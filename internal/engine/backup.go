package engine

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const rconRetries = 5

var rconRetryInterval = 10 * time.Second

func sshBaseArgs(nas NASConfig) []string {
	args := []string{"ssh"}
	if nas.SSHPort != 0 && nas.SSHPort != 22 {
		args = append(args, "-p", fmt.Sprintf("%d", nas.SSHPort))
	}
	if nas.SSHKey != "" {
		args = append(args, "-i", os.ExpandEnv(nas.SSHKey))
	}
	args = append(args, "-o", "BatchMode=yes", "-o", "ConnectTimeout=10", "-o", "ServerAliveInterval=15", "-o", "ServerAliveCountMax=3")
	return args
}

func localRsyncArgs(dataDir, prevBackup, destDir string, excludes []string) []string {
	args := []string{"rsync", "-a", "--timeout=300"}
	if prevBackup != "" {
		args = append(args, fmt.Sprintf("--link-dest=%s", prevBackup))
	}
	for _, ex := range excludes {
		args = append(args, fmt.Sprintf("--exclude=%s", ex))
	}
	args = append(args, dataDir, destDir+"/")
	return args
}

func nasRsyncArgs(dataDir, prevBackup, destDir string, nas NASConfig, maxMbps float64, excludes []string) []string {
	sshRemote := fmt.Sprintf("%s@%s", nas.SSHUser, nas.SSHHost)
	args := []string{"rsync", "-a", "--timeout=300"}
	if maxMbps > 0 {
		args = append(args, fmt.Sprintf("--bwlimit=%d", int(maxMbps*1024)))
	}
	if prevBackup != "" {
		args = append(args, fmt.Sprintf("--link-dest=%s", prevBackup))
	}
	for _, ex := range excludes {
		args = append(args, fmt.Sprintf("--exclude=%s", ex))
	}
	sshArgs := sshBaseArgs(nas)
	args = append(args, "-e", strings.Join(sshArgs, " "))
	args = append(args, dataDir, fmt.Sprintf("%s:%s/", sshRemote, destDir))
	return args
}

func runRsync(ctx context.Context, args []string, onProgress func(bytesMoved, totalSize int64)) error {
	if onProgress != nil {
		newArgs := make([]string, 0, len(args)+1)
		newArgs = append(newArgs, args[0], "--info=progress2")
		newArgs = append(newArgs, args[1:]...)
		args = newArgs
	}

	slog.Debug("running rsync", "args", args)
	//nolint:gosec // G204: rsync binary and options are constructed internally by backup engine
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)

	if onProgress != nil {
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return err
		}
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			return err
		}
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			streamRsyncProgress(stdout, onProgress)
			wg.Done()
		}()
		err = cmd.Wait()
		stdout.Close()
		wg.Wait()
		return err
	}

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

var rsyncRunner = runRsync

func runSync(ctx context.Context) {
	cmd := commandRunner.CommandContext(ctx, "sync")
	if err := cmd.Run(); err != nil {
		slog.Warn("filesystem sync failed", "error", err)
	}
}

func checkNASReady(ctx context.Context, nas NASConfig) error {
	sentinel := fmt.Sprintf("%s/.nas-ready", nas.DestRoot)
	if err := runSSH(ctx, nas, nasReadyCommand(nas)); err != nil {
		return fmt.Errorf("NAS sentinel %s not found", sentinel)
	}
	return nil
}

func nasReadyCommand(nas NASConfig) string {
	return fmt.Sprintf("test -f %s", shellQuote(fmt.Sprintf("%s/.nas-ready", nas.DestRoot)))
}

func nasMkdirCommand(path string) string {
	return fmt.Sprintf("mkdir -p %s", shellQuote(path))
}

func nasRemoveDirCommand(path string) string {
	return fmt.Sprintf("rm -rf -- %s", shellQuote(path))
}

func nasRenameCommand(from, to string) string {
	return fmt.Sprintf("mv -- %s %s", shellQuote(from), shellQuote(to))
}

func ensureNASDir(ctx context.Context, nas NASConfig, path string) error {
	return runSSH(ctx, nas, nasMkdirCommand(path))
}

func isBackupDir(name string) bool {
	if len(name) != 13 {
		return false
	}
	if name[8] != '-' {
		return false
	}
	for i, c := range name {
		if i == 8 {
			continue
		}
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

type backupResult struct {
	destination    string
	timestamp      string
	target         string
	offloadPending bool
}

type BackupEngine struct {
	cfg        Config
	OnProgress func(bytesMoved, totalSize int64)
}

func NewBackupEngine(cfg Config) *BackupEngine {
	return &BackupEngine{cfg: cfg}
}

func (be *BackupEngine) BackupServer(ctx context.Context, watch WatchConfig, serverName string, server ServerConfig, prevHotBackup, prevLocalBackup, prevNASBackup string, offline bool) (result backupResult, rerr error) {
	target, err := resolveBackupTarget(serverName, server, be.cfg.Local, be.cfg.NAS)
	if err != nil {
		return backupResult{}, err
	}

	dataDir := server.DataDir
	if dataDir == "" {
		dataDir = filepath.Join(watch.Path, serverName, "mc-data")
	}
	excludes := be.cfg.ResolveServerExcludes(server)

	container := server.ContainerName
	if container == "" {
		container = serverName + "-mc-1"
	}

	if !offline {
		rconPass := resolveRconPassword(server, watch, serverName)
		if err := runRcon(ctx, container, rconPass, "save-off", rconRetries, rconRetryInterval); err != nil {
			return backupResult{}, fmt.Errorf("save-off: %w", err)
		}

		defer func() {
			slog.Info("re-enabling autosave", "server", serverName)
			detachedCtx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			defer cancel()
			if err := runRcon(detachedCtx, container, rconPass, "save-on", rconRetries, rconRetryInterval); err != nil {
				saveOnErr := fmt.Errorf("FATAL: save-on failed for %s: %w", serverName, err)
				slog.Error(saveOnErr.Error())
				if rerr == nil {
					rerr = saveOnErr
				} else {
					slog.Error("save-on failed after backup error, server may have autosave OFF", "server", serverName, "backup_error", rerr)
				}
			}
		}()

		if err := runRcon(ctx, container, rconPass, "save-all flush", rconRetries, rconRetryInterval); err != nil {
			return backupResult{}, fmt.Errorf("save-all flush: %w", err)
		}
	} else {
		slog.Info("offline backup, skipping RCON", "server", serverName)
	}

	runSync(ctx)

	ts := time.Now().Format("20060102-1504")

	switch target {
	case "nas":
		if err := checkNASReady(ctx, be.cfg.NAS); err != nil {
			return backupResult{}, fmt.Errorf("NAS not ready: %w", err)
		}
		destDir := fmt.Sprintf("%s/%s/%s/%s", be.cfg.NAS.DestRoot, watch.Namespace, serverName, ts)
		parent := destDir[:strings.LastIndex(destDir, "/")]
		if err := ensureNASDir(ctx, be.cfg.NAS, parent); err != nil {
			return backupResult{}, fmt.Errorf("create NAS dir: %w", err)
		}
		args := nasRsyncArgs(dataDir, prevNASBackup, destDir, be.cfg.NAS, be.cfg.Global.MaxMBps, excludes)
		if err := rsyncRunner(ctx, args, be.OnProgress); err != nil {
			return backupResult{}, fmt.Errorf("NAS rsync: %w", err)
		}
		slog.Info("backup complete", "server", serverName, "target", target, "dest", destDir)
		return backupResult{destination: destDir, timestamp: ts, target: target, offloadPending: false}, nil

	case "local":
		destPath := filepath.Join(be.cfg.Local.DestRoot, watch.Namespace, serverName, ts)
		if err := os.MkdirAll(destPath, 0755); err != nil {
			return backupResult{}, fmt.Errorf("local mkdir: %w", err)
		}
		args := localRsyncArgs(dataDir, prevLocalBackup, destPath, excludes)
		if err := rsyncRunner(ctx, args, be.OnProgress); err != nil {
			return backupResult{}, fmt.Errorf("local rsync: %w", err)
		}
		slog.Info("backup complete", "server", serverName, "target", target, "dest", destPath)
		return backupResult{destination: destPath, timestamp: ts, target: target, offloadPending: false}, nil

	case "tiered-local", "tiered-nas":
		hotDir := filepath.Join(be.cfg.Local.HotRoot, watch.Namespace, serverName)
		inProgressPath := filepath.Join(hotDir, ts+".inprogress")
		finalPath := filepath.Join(hotDir, ts)

		os.RemoveAll(inProgressPath)
		if err := os.MkdirAll(hotDir, 0755); err != nil {
			return backupResult{}, fmt.Errorf("hot mkdir: %w", err)
		}
		if err := os.MkdirAll(inProgressPath, 0755); err != nil {
			return backupResult{}, fmt.Errorf("hot inprogress mkdir: %w", err)
		}

		args := localRsyncArgs(dataDir, prevHotBackup, inProgressPath, excludes)
		if err := rsyncRunner(ctx, args, be.OnProgress); err != nil {
			os.RemoveAll(inProgressPath)
			return backupResult{}, fmt.Errorf("hot rsync: %w", err)
		}

		if err := os.Rename(inProgressPath, finalPath); err != nil {
			os.RemoveAll(inProgressPath)
			return backupResult{}, fmt.Errorf("hot rename: %w", err)
		}

		slog.Info("hot backup complete", "server", serverName, "target", target, "dest", finalPath)
		return backupResult{destination: finalPath, timestamp: ts, target: target, offloadPending: true}, nil

	default:
		return backupResult{}, fmt.Errorf("unsupported backup target %q", target)
	}
}

func offloadSnapshot(
	ctx context.Context,
	cfg Config,
	watch WatchConfig,
	serverName, hotPath, timestamp, coldTarget, prevColdBackup string,
	onProgress func(bytesMoved, totalSize int64),
) (destPath string, err error) {
	srcDir := strings.TrimRight(hotPath, "/") + "/"

	if coldTarget == "local" {
		destDir := filepath.Join(cfg.Local.DestRoot, watch.Namespace, serverName)
		inProgress := filepath.Join(destDir, timestamp+".inprogress")
		finalPath := filepath.Join(destDir, timestamp)

		os.RemoveAll(inProgress)
		if err := os.MkdirAll(inProgress, 0755); err != nil {
			return "", fmt.Errorf("local offload mkdir: %w", err)
		}

		cleaned := false
		defer func() {
			if !cleaned {
				os.RemoveAll(inProgress)
			}
		}()

		args := localRsyncArgs(srcDir, prevColdBackup, inProgress, nil)
		if err := rsyncRunner(ctx, args, onProgress); err != nil {
			return "", fmt.Errorf("local offload rsync: %w", err)
		}

		if err := os.Rename(inProgress, finalPath); err != nil {
			return "", fmt.Errorf("local offload rename: %w", err)
		}
		cleaned = true
		return finalPath, nil

	} else if coldTarget == "nas" {
		if err := checkNASReady(ctx, cfg.NAS); err != nil {
			return "", fmt.Errorf("NAS not ready for offload: %w", err)
		}

		remoteServerDir := fmt.Sprintf("%s/%s/%s", cfg.NAS.DestRoot, watch.Namespace, serverName)
		remoteInProgress := fmt.Sprintf("%s/%s.inprogress", remoteServerDir, timestamp)
		remoteFinal := fmt.Sprintf("%s/%s", remoteServerDir, timestamp)

		if err := ensureNASDir(ctx, cfg.NAS, remoteServerDir); err != nil {
			return "", fmt.Errorf("create NAS offload dir: %w", err)
		}

		_ = runSSH(ctx, cfg.NAS, nasRemoveDirCommand(remoteInProgress))

		cleaned := false
		defer func() {
			if !cleaned {
				cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				if rmErr := runSSH(cleanupCtx, cfg.NAS, nasRemoveDirCommand(remoteInProgress)); rmErr != nil {
					slog.Warn("failed to cleanup remote inprogress offload", "path", remoteInProgress, "error", rmErr)
				}
			}
		}()

		args := nasRsyncArgs(srcDir, prevColdBackup, remoteInProgress, cfg.NAS, cfg.Global.MaxMBps, nil)
		if err := rsyncRunner(ctx, args, onProgress); err != nil {
			return "", fmt.Errorf("NAS offload rsync: %w", err)
		}

		if err := runSSH(ctx, cfg.NAS, nasRenameCommand(remoteInProgress, remoteFinal)); err != nil {
			return "", fmt.Errorf("NAS offload rename: %w", err)
		}
		cleaned = true
		return remoteFinal, nil
	}

	return "", fmt.Errorf("unsupported cold target %q", coldTarget)
}

func parseRsyncProgress(line string) (bytesMoved int64, totalSize int64, ok bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return 0, 0, false
	}
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return 0, 0, false
	}
	cleaned := strings.ReplaceAll(fields[0], ",", "")
	bytes, err := strconv.ParseInt(cleaned, 10, 64)
	if err != nil {
		return 0, 0, false
	}
	pctStr := strings.TrimSuffix(fields[1], "%")
	pct, err := strconv.ParseInt(pctStr, 10, 64)
	if err != nil || pct == 0 {
		return bytes, 0, true
	}
	return bytes, bytes * 100 / pct, true
}

func scanRsyncLines(data []byte, atEOF bool) (advance int, token []byte, err error) {
	for i, b := range data {
		if b == '\r' || b == '\n' {
			adv := i + 1
			if b == '\r' && i+1 < len(data) && data[i+1] == '\n' {
				adv = i + 2
			}
			return adv, data[:i], nil
		}
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}

func streamRsyncProgress(r io.Reader, onProgress func(bytesMoved, totalSize int64)) {
	scanner := bufio.NewScanner(r)
	scanner.Split(scanRsyncLines)
	for scanner.Scan() {
		if bytes, total, ok := parseRsyncProgress(scanner.Text()); ok {
			onProgress(bytes, total)
		}
	}
	if err := scanner.Err(); err != nil {
		slog.Warn("rsync progress scanner error", "error", err)
	}
}
