package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	targetBin = "/system/bin/kokoni_web"
	workDir   = "/data/local/kokoni_agent"
	logPath   = "/data/local/kokoni_agent/current.log"
	pidPath   = "/data/local/kokoni_agent/kokoni_web.pid"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	switch os.Args[1] {
	case "start":
		if err := start(); err != nil {
			fmt.Fprintln(os.Stderr, "start failed:", err)
			os.Exit(1)
		}
	case "stop":
		if err := stop(); err != nil {
			fmt.Fprintln(os.Stderr, "stop failed:", err)
			os.Exit(1)
		}
	case "status":
		if err := status(); err != nil {
			fmt.Fprintln(os.Stderr, "status failed:", err)
			os.Exit(1)
		}
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Println("usage: kokoni_launcher start|stop|status")
}

func start() error {
	if err := os.MkdirAll(filepath.Join(workDir, "jobs", "uploaded"), 0777); err != nil {
		return err
	}
	_ = os.Chmod(workDir, 0777)
	_ = os.Chmod(filepath.Join(workDir, "jobs"), 0777)
	_ = os.Chmod(filepath.Join(workDir, "jobs", "uploaded"), 0777)

	if pid, ok := findRunningKokoniWeb(); ok {
		fmt.Printf("already running pid=%d\n", pid)
		return nil
	}

	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		return err
	}
	defer logFile.Close()

	nullFile, err := os.OpenFile("/dev/null", os.O_RDONLY, 0)
	if err != nil {
		return err
	}
	defer nullFile.Close()

	files := []uintptr{
		nullFile.Fd(),
		logFile.Fd(),
		logFile.Fd(),
	}

	attr := &syscall.ProcAttr{
		Dir: workDir,
		Env: []string{
			"PATH=/sbin:/system/sbin:/system/bin:/system/xbin",
		},
		Files: files,
		Sys: &syscall.SysProcAttr{
			Setsid: true,
		},
	}

	pid, err := syscall.ForkExec(targetBin, []string{targetBin}, attr)
	if err != nil {
		return err
	}

	_ = os.WriteFile(pidPath, []byte(strconv.Itoa(pid)+"\n"), 0666)

	time.Sleep(500 * time.Millisecond)

	if runningPid, ok := findRunningKokoniWeb(); ok {
		fmt.Printf("started pid=%d\n", runningPid)
		return nil
	}

	return errors.New("process did not remain running")
}

func stop() error {
	pid, ok := findRunningKokoniWeb()
	if !ok {
		fmt.Println("not running")
		_ = os.Remove(pidPath)
		return nil
	}

	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		return err
	}

	time.Sleep(800 * time.Millisecond)

	if _, still := findRunningKokoniWeb(); still {
		_ = syscall.Kill(pid, syscall.SIGKILL)
		time.Sleep(300 * time.Millisecond)
	}

	_ = os.Remove(pidPath)
	fmt.Printf("stopped pid=%d\n", pid)
	return nil
}

func status() error {
	pid, ok := findRunningKokoniWeb()
	if !ok {
		fmt.Println("stopped")
		return nil
	}

	fmt.Printf("running pid=%d\n", pid)
	return nil
}

func findRunningKokoniWeb() (int, bool) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0, false
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}

		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}

		cmdlinePath := filepath.Join("/proc", e.Name(), "cmdline")
		f, err := os.Open(cmdlinePath)
		if err != nil {
			continue
		}

		b, _ := io.ReadAll(f)
		_ = f.Close()

		cmd := strings.ReplaceAll(string(b), "\x00", " ")
		if strings.Contains(cmd, "kokoni_web") && !strings.Contains(cmd, "kokoni_launcher") {
			return pid, true
		}
	}

	return 0, false
}
