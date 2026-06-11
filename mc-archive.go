//go:build ignore

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"text/tabwriter"
	"time"
)

// ServerRoot defines an SSD root directory and its corresponding namespace on the HDD
type ServerRoot struct {
	Path         string
	HDDNamespace string
}

const MaxMBps = 40.0 // Maximum Megabytes per second to transfer

// BlacklistedDirs contains server directory names to exclude from all watching,
// provisioning, and archive processing.
// Add directory names here to make the daemon completely ignore them.
var BlacklistedDirs = []string{
	"cone-ftb-evo",
}

// isBlacklisted returns true if the directory name matches an entry in the blacklist.
func isBlacklisted(name string) bool {
	for _, blocked := range BlacklistedDirs {
		if name == blocked {
			return true
		}
	}
	return false
}

var (
	// LXC specific paths
	SSDRoots = []ServerRoot{
		{
			Path:         "/opt/minecraft/servers/docker/servers",
			HDDNamespace: "minecraft",
		},
		{
			Path:         "/opt/minecraft/servers/david-docker/servers",
			HDDNamespace: "minecraft-david",
		},
	}

	HDDRoot = "/mnt/hdd-archives"

	LocalRetention   = 3
	HDDRetentionTime = 4 * 24 * time.Hour
	GrowthPollFreq   = 10 * time.Second
	MainScanFreq     = 1 * time.Minute
	CleanupFreq      = 6 * time.Hour

	// Concurrency and Queuing
	hddWriteLock = make(chan struct{}, 1)

	// Job Tracking for the CLI Status
	jobsMutex sync.RWMutex
	jobsQueue = make(map[string]*JobInfo)
)

type JobInfo struct {
	ServerName string `json:"server_name"`
	FileName   string `json:"file_name"`
	State      string `json:"state"`
	BytesMoved int64  `json:"bytes_moved"`
	TotalSize  int64  `json:"total_size"`
}

func main() {
	// If executed with "daemon", run the background service.
	// Otherwise, act as the CLI status tool.
	if len(os.Args) > 1 && os.Args[1] == "daemon" {
		runDaemon()
	} else {
		printCLIStatus()
	}
}

// =====================================================================
// CLI DASHBOARD
// =====================================================================

func printCLIStatus() {
	resp, err := http.Get("http://127.0.0.1:47990/status")
	if err != nil {
		fmt.Println("❌ Error: Could not connect to mc-archive daemon. Is the service running?")
		return
	}
	defer resp.Body.Close()

	var activeJobs map[string]*JobInfo
	if err := json.NewDecoder(resp.Body).Decode(&activeJobs); err != nil {
		fmt.Println("❌ Error parsing daemon response.")
		return
	}

	fmt.Println("\n📦 [Minecraft Archive Manager - Live Queue]")

	// Display blacklisted directories with resolved full paths
	if len(BlacklistedDirs) > 0 {
		fmt.Println("\n🚫 Blacklisted Directories:")
		for _, blocked := range BlacklistedDirs {
			for _, root := range SSDRoots {
				fmt.Printf("   %s\n", filepath.Join(root.Path, blocked))
			}
		}
	} else {
		fmt.Println("✅ No directories are currently blacklisted.")
	}

	fmt.Println(strings.Repeat("-", 85))

	if len(activeJobs) == 0 {
		fmt.Println("   Queue is currently empty. No active transfers or pending checks.")
		fmt.Println(strings.Repeat("-", 85))
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "SERVER\tFILE\tSTATE\tPROGRESS\t")

	for _, job := range activeJobs {
		progress := fmt.Sprintf("%.2fG / %.2fG", float64(job.BytesMoved)/1073741824.0, float64(job.TotalSize)/1073741824.0)
		if job.TotalSize == 0 {
			progress = "Calculating..."
		}

		serverTrunc := job.ServerName
		if len(serverTrunc) > 15 {
			serverTrunc = serverTrunc[:12] + "..."
		}

		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t\n", serverTrunc, job.FileName, job.State, progress)
	}
	w.Flush()
	fmt.Println(strings.Repeat("-", 85))
}

// =====================================================================
// DAEMON MODE
// =====================================================================

func runDaemon() {
	log.Println("[INFO] Starting Multi-Root LXC Storage Provisioning & Archive Manager Daemon...")

	// Start the internal status API for the CLI
	go func() {
		http.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
			jobsMutex.RLock()
			defer jobsMutex.RUnlock()
			json.NewEncoder(w).Encode(jobsQueue)
		})
		log.Fatal(http.ListenAndServe("127.0.0.1:47990", nil))
	}()

	scanTicker := time.NewTicker(MainScanFreq)
	cleanupTicker := time.NewTicker(CleanupFreq)

	runCycle()
	go cleanupHDDDir()

	for {
		select {
		case <-scanTicker.C:
			runCycle()
		case <-cleanupTicker.C:
			go cleanupHDDDir()
		}
	}
}

func runCycle() {
	sentinel := filepath.Join(HDDRoot, ".hdd-ready")
	if _, err := os.Stat(sentinel); os.IsNotExist(err) {
		log.Printf("[CRITICAL] Sentinel file %s missing! HDD/NFS unmounted. Pausing operations.\n", sentinel)
		return
	}

	for _, root := range SSDRoots {
		provisionRoot(root)
		processLocalBackups(root)
	}
}

func isArchive(filename string) bool {
	name := strings.ToLower(filename)
	return strings.HasSuffix(name, ".tar.gz") || strings.HasSuffix(name, ".tar.bz2") ||
		strings.HasSuffix(name, ".zip") || strings.HasSuffix(name, ".tar")
}

func provisionRoot(root ServerRoot) {
	entries, err := os.ReadDir(root.Path)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		serverName := entry.Name()
		if isBlacklisted(serverName) {
			log.Printf("[SKIP] Blacklisted directory ignored: %s/%s\n", root.HDDNamespace, serverName)
			continue
		}
		serverDir := filepath.Join(root.Path, serverName)
		symlinkPath := filepath.Join(serverDir, "backups-hdd")
		hddTarget := filepath.Join(HDDRoot, root.HDDNamespace, serverName)

		if _, err := os.Lstat(symlinkPath); os.IsNotExist(err) {
			log.Printf("[PROVISION] Detecting new server in %s: %s. Setting up HDD storage...\n", root.HDDNamespace, serverName)
			os.MkdirAll(hddTarget, 0755)
			os.Symlink(hddTarget, symlinkPath)
			log.Printf("[SUCCESS] Provisioned HDD resources for %s/%s\n", root.HDDNamespace, serverName)
		}
	}
}

func processLocalBackups(root ServerRoot) {
	entries, err := os.ReadDir(root.Path)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		serverName := entry.Name()
		if isBlacklisted(serverName) {
			continue
		}
		backupsDir := filepath.Join(root.Path, serverName, "backups")

		if _, err := os.Stat(backupsDir); os.IsNotExist(err) {
			continue
		}

		backupFiles, err := os.ReadDir(backupsDir)
		if err != nil {
			continue
		}

		var validArchives []os.FileInfo
		for _, f := range backupFiles {
			if isArchive(f.Name()) {
				if info, err := f.Info(); err == nil {
					validArchives = append(validArchives, info)
				}
			}
		}

		sort.Slice(validArchives, func(i, j int) bool {
			return validArchives[i].ModTime().After(validArchives[j].ModTime())
		})

		if len(validArchives) > LocalRetention {
			oldArchives := validArchives[LocalRetention:]
			for _, archive := range oldArchives {
				sourcePath := filepath.Join(backupsDir, archive.Name())
				destPath := filepath.Join(root.Path, serverName, "backups-hdd", archive.Name())

				jobsMutex.RLock()
				_, processing := jobsQueue[sourcePath]
				jobsMutex.RUnlock()

				if processing {
					continue
				}

				log.Printf("[QUEUE] Adding %s to the queue.", archive.Name())

				jobsMutex.Lock()
				jobsQueue[sourcePath] = &JobInfo{
					ServerName: serverName,
					FileName:   archive.Name(),
					State:      "Growth Polling",
					TotalSize:  archive.Size(),
				}
				jobsMutex.Unlock()

				go handleArchiveTransfer(sourcePath, destPath, serverName)
			}
		}
	}
}

func handleArchiveTransfer(src, dst, serverName string) {
	defer func() {
		jobsMutex.Lock()
		delete(jobsQueue, src)
		jobsMutex.Unlock()
	}()

	if !isDoneGrowing(src) {
		log.Printf("[WAIT] Archive %s is still growing. Delaying lock.\n", filepath.Base(src))
		return
	}

	updateJobState(src, "Queued (Waiting Lock)")
	log.Printf("[QUEUE] %s ready. Waiting for global HDD write lock...\n", filepath.Base(src))

	hddWriteLock <- struct{}{}
	defer func() { <-hddWriteLock }()

	updateJobState(src, "Transferring")
	log.Printf("[MOVE] Lock acquired! Moving %s to HDD storage...\n", filepath.Base(src))

	if err := moveCrossDevice(src, dst); err != nil {
		log.Printf("[ERROR] Transfer failed for %s: %v\n", filepath.Base(src), err)
		return
	}
	log.Printf("[SUCCESS] Archived %s successfully.\n", filepath.Base(src))
}

func updateJobState(src, state string) {
	jobsMutex.Lock()
	if job, ok := jobsQueue[src]; ok {
		job.State = state
	}
	jobsMutex.Unlock()
}

func isDoneGrowing(filePath string) bool {
	info1, err := os.Stat(filePath)
	if err != nil {
		return false
	}
	size1 := info1.Size()

	time.Sleep(GrowthPollFreq)

	info2, err := os.Stat(filePath)
	if err != nil {
		return false
	}
	size2 := info2.Size()

	return size1 == size2 && size1 > 0
}

// ---------------------------------------------------------------------
// Byte-Tracking IO Wrapper
// ---------------------------------------------------------------------

type ProgressReader struct {
	io.Reader
	JobPath string
}

func (pr *ProgressReader) Read(p []byte) (int, error) {
	n, err := pr.Reader.Read(p)
	if n > 0 {
		// --- THE NEW THROTTLE INJECTION ---
		// Calculate how long to sleep to maintain the target speed limit
		// n bytes / (MaxMBps * 1,000,000 bytes) = seconds to sleep
		sleepDuration := time.Duration((float64(n) / (MaxMBps * 1000000.0)) * float64(time.Second))
		time.Sleep(sleepDuration)
		// ----------------------------------
		jobsMutex.RLock()
		job, ok := jobsQueue[pr.JobPath]
		jobsMutex.RUnlock()
		if ok {
			atomic.AddInt64(&job.BytesMoved, int64(n))
		}
	}
	return n, err
}

func moveCrossDevice(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}

	out, err := os.Create(dst)
	if err != nil {
		in.Close()
		return err
	}

	reader := &ProgressReader{
		Reader:  in,
		JobPath: src,
	}

	if _, err = io.Copy(out, reader); err != nil {
		in.Close()
		out.Close()
		return err
	}

	err = out.Sync()
	in.Close()
	out.Close()
	if err != nil {
		return err
	}

	return os.Remove(src)
}

func cleanupHDDDir() {
	sentinel := filepath.Join(HDDRoot, ".hdd-ready")
	if _, err := os.Stat(sentinel); os.IsNotExist(err) {
		return
	}

	log.Println("[CLEANUP] Scanning HDD Archives for outdated backups...")

	filepath.Walk(HDDRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() && isArchive(info.Name()) {
			if time.Since(info.ModTime()) > HDDRetentionTime {
				if err := os.Remove(path); err == nil {
					log.Printf("[CLEANUP] Deleted old archive: %s\n", path)
				}
			}
		}
		return nil
	})
}
