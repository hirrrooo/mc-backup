package engine

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"log/slog"
)

func sshBaseArgs(nas NASConfig) []string {
	args := []string{"ssh"}
	if nas.SSHPort != 0 && nas.SSHPort != 22 {
		args = append(args, "-p", fmt.Sprintf("%d", nas.SSHPort))
	}
	if nas.SSHKey != "" {
		args = append(args, "-i", os.ExpandEnv(nas.SSHKey))
	}
	args = append(args, "-o", "BatchMode=yes", "-o", "ConnectTimeout=10")
	return args
}

func localRsyncArgs(dataDir, prevBackup, destDir string, excludes []string) []string {
	args := []string{"rsync", "-a"}
	if prevBackup != "" {
		args = append(args, fmt.Sprintf("--link-dest=%s", prevBackup))
	}
	for _, ex := range excludes {
		args = append(args, fmt.Sprintf("--exclude=%s", ex))
	}
	args = append(args, dataDir+"/", destDir+"/")
	return args
}

func nasRsyncArgs(dataDir, prevBackup, destDir string, nas NASConfig, maxMbps float64, excludes []string) []string {
	sshRemote := fmt.Sprintf("%s@%s", nas.SSHUser, nas.SSHHost)
	args := []string{"rsync", "-a"}
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
	args = append(args, dataDir+"/", fmt.Sprintf("%s:%s/", sshRemote, destDir))
	return args
}

func runRsync(ctx context.Context, args []string) error {
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	slog.Debug("running rsync", "args", args)
	return cmd.Run()
}

func checkNASReady(ctx context.Context, nas NASConfig) error {
	sentinel := fmt.Sprintf("%s/.nas-ready", nas.DestRoot)
	args := sshBaseArgs(nas)
	args = append(args, fmt.Sprintf("%s@%s", nas.SSHUser, nas.SSHHost),
		fmt.Sprintf("test -f %s", sentinel),
	)
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("NAS sentinel %s not found", sentinel)
	}
	return nil
}

func ensureNASDir(ctx context.Context, nas NASConfig, path string) error {
	args := sshBaseArgs(nas)
	args = append(args, fmt.Sprintf("%s@%s", nas.SSHUser, nas.SSHHost),
		fmt.Sprintf("mkdir -p %s", path),
	)
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	return cmd.Run()
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

const rconRetries = 5
const rconRetryInterval = 10 * time.Second

type BackupEngine struct {
	cfg Config
}

func NewBackupEngine(cfg Config) *BackupEngine {
	return &BackupEngine{cfg: cfg}
}

func (be *BackupEngine) BackupServer(ctx context.Context, watch WatchConfig, serverName string, server ServerConfig, prevLocalBackup, prevNASBackup string) (destPath string, usedSSH bool, rerr error) {
	dataDir := server.DataDir
	if dataDir == "" {
		dataDir = filepath.Join(watch.Path, serverName, "mc-data")
	}
	excludes := []string{"*.jar", "cache", "logs", "*.tmp"}

	container := server.ContainerName
	if container == "" {
		container = serverName + "-mc-1"
	}

	if err := runRcon(ctx, container, server.RconPassword, "save-off", rconRetries, rconRetryInterval); err != nil {
		return "", false, fmt.Errorf("save-off: %w", err)
	}

	defer func() {
		slog.Info("re-enabling autosave", "server", serverName)
		detachedCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		if err := runRcon(detachedCtx, container, server.RconPassword, "save-on", rconRetries, rconRetryInterval); err != nil {
			saveOnErr := fmt.Errorf("FATAL: save-on failed for %s: %w", serverName, err)
			slog.Error(saveOnErr.Error())
			if rerr == nil {
				rerr = saveOnErr
			} else {
				slog.Error("save-on failed after backup error, server may have autosave OFF", "server", serverName, "backup_error", rerr)
			}
		}
	}()

	if err := runRcon(ctx, container, server.RconPassword, "save-all flush", rconRetries, rconRetryInterval); err != nil {
		return "", false, fmt.Errorf("save-all flush: %w", err)
	}

	exec.Command("sync").Run()

	ts := time.Now().Format("20060102-1504")

	useSSH := server.SSHOnly
	if !useSSH {
		backupDir := watch.backupDir(serverName)
		if pct, err := diskUsagePct(backupDir); err == nil {
			estSize, _ := dirSize(dataDir)
			totalSpace := totalDiskSpace(backupDir)
			if totalSpace > 0 {
				neededPct := (float64(estSize) / float64(totalSpace)) * 100.0
				if freePct := 100.0 - pct; freePct-neededPct < float64(100-watch.MaxDiskPct) {
					slog.Info("SSD usage projected above threshold, routing to NAS",
						"server", serverName, "current_pct", pct, "estimated_pct", pct+neededPct)
					useSSH = true
				}
			}
		}
	}

	if useSSH {
		if err := checkNASReady(ctx, be.cfg.NAS); err != nil {
			return "", false, fmt.Errorf("NAS not ready: %w", err)
		}
		destDir := fmt.Sprintf("%s/%s/%s/%s", be.cfg.NAS.DestRoot, watch.Namespace, serverName, ts)
		parent := destDir[:strings.LastIndex(destDir, "/")]
		if err := ensureNASDir(ctx, be.cfg.NAS, parent); err != nil {
			return "", false, fmt.Errorf("create NAS dir: %w", err)
		}
		args := nasRsyncArgs(dataDir, prevNASBackup, destDir, be.cfg.NAS, be.cfg.Global.MaxMBps, excludes)
		if err := runRsync(ctx, args); err != nil {
			return "", false, fmt.Errorf("NAS rsync: %w", err)
		}
		destPath = destDir
	} else {
		backupDir := watch.backupDir(serverName)
		destPath = filepath.Join(backupDir, ts)
		if err := os.MkdirAll(destPath, 0755); err != nil {
			return "", false, fmt.Errorf("mkdir: %w", err)
		}
		args := localRsyncArgs(dataDir, prevLocalBackup, destPath, excludes)
		if err := runRsync(ctx, args); err != nil {
			return "", false, fmt.Errorf("local rsync: %w", err)
		}
	}

	slog.Info("backup complete", "server", serverName, "dest", destPath, "ssh_only", useSSH)
	return destPath, useSSH, nil
}
