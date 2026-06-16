package engine

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"syscall"
)

func diskUsagePct(path string) (float64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, err
	}
	total := stat.Blocks * uint64(stat.Bsize)
	free := stat.Bavail * uint64(stat.Bsize)
	used := total - free
	if total == 0 {
		return 0, nil
	}
	return (float64(used) / float64(total)) * 100.0, nil
}

func totalDiskSpace(path string) uint64 {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0
	}
	return stat.Blocks * uint64(stat.Bsize)
}

func dirSize(path string, excludes []string) (int64, error) {
	args := []string{"du", "-sb"}
	for _, ex := range excludes {
		args = append(args, "--exclude="+ex)
	}
	args = append(args, path)
	cmd := commandRunner.CommandContext(context.Background(), args[0], args[1:]...)
	out, err := cmd.Output()
	if err != nil {
		return 0, err
	}
	fields := strings.Fields(string(out))
	if len(fields) < 1 {
		return 0, fmt.Errorf("du: unexpected output: %s", string(out))
	}
	size, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("du: parse size: %w", err)
	}
	return size, nil
}
