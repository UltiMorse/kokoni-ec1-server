package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"kokoni-ec1-server/internal/serial"
)

const (
	pauseLiftMM = 10
	listenAddr  = ":8080"

	uartDev  = "/dev/ttyS1"
	uartBaud = 115200

	stateDir  = "/data/local/kokoni_agent"
	statePath = "/data/local/kokoni_agent/state.json"
	logPath   = "/data/local/kokoni_agent/current.log"

	jobsDir      = "/data/local/kokoni_agent/jobs"
	currentGCode = "/data/local/kokoni_agent/jobs/current.gcode"

	version = "job-api-v1"

	defaultOKTimeout  = 30 * time.Second
	heatingOKTimeout  = 15 * time.Minute
	maxScannerBufSize = 1024 * 1024
	lineDelay         = 10 * time.Millisecond
)

var (
	startedAt = time.Now()

	uart   *serial.UARTController
	uartMu sync.Mutex

	commandMu sync.Mutex
	okChan    = make(chan struct{}, 100)

	statusMu      sync.Mutex
	printerState  = "unknown"
	lastMCULine   = ""
	uartConnected = false

	jobMu sync.Mutex

	pendingLightMu    sync.Mutex
	pendingLightValue *int

	job = JobStatus{
		State: "idle",
		Path:  currentGCode,
	}
)

type Status struct {
	Agent         string    `json:"agent"`
	State         string    `json:"state"`
	Printer       string    `json:"printer"`
	UARTConnected bool      `json:"uart_connected"`
	LastMCULine   string    `json:"last_mcu_line"`
	Version       string    `json:"version"`
	GoVersion     string    `json:"go_version"`
	UptimeSec     int64     `json:"uptime_sec"`
	StartedAt     string    `json:"started_at"`
	StatePath     string    `json:"state_path"`
	LogPath       string    `json:"log_path"`
	Job           JobStatus `json:"job"`
}

type JobStatus struct {
	State       string       `json:"state"` // idle, uploaded, printing, paused, done, cancelled, error
	FileName    string       `json:"file_name"`
	Path        string       `json:"path"`
	TotalLines  int          `json:"total_lines"`
	CurrentLine int          `json:"current_line"`
	ProgressPct int          `json:"progress_pct"`
	Cancel      bool         `json:"cancel"`
	Error       string       `json:"error"`
	StartedAt   string       `json:"started_at"`
	UpdatedAt   string       `json:"updated_at"`
	LastCommand string       `json:"last_command"`
	PauseLifted bool         `json:"pause_lifted"`
	Summary     GCodeSummary `json:"summary"`
}

type GCodeSummary struct {
	EstimatedTimeSec int       `json:"estimated_time_sec"`
	FilamentMM       float64   `json:"filament_mm"`
	LayerCount       int       `json:"layer_count"`
	NozzleTemps      []float64 `json:"nozzle_temps"`
	BedTemps         []float64 `json:"bed_temps"`
	HasHome          bool      `json:"has_home"`
	HasM104          bool      `json:"has_m104"`
	HasM109          bool      `json:"has_m109"`
	HasM140          bool      `json:"has_m140"`
	HasM106          bool      `json:"has_m106"`
	HasBounds        bool      `json:"has_bounds"`
	MinX             float64   `json:"min_x"`
	MaxX             float64   `json:"max_x"`
	MinY             float64   `json:"min_y"`
	MaxY             float64   `json:"max_y"`
	MinZ             float64   `json:"min_z"`
	MaxZ             float64   `json:"max_z"`
	MinE             float64   `json:"min_e"`
	MaxE             float64   `json:"max_e"`
	Warnings         []string  `json:"warnings"`
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func main() {
	if err := os.MkdirAll(jobsDir, 0777); err != nil {
		log.Printf("mkdir jobs failed: %v", err)
	}
	if err := os.MkdirAll(stateDir, 0777); err != nil {
		log.Printf("mkdir state dir failed: %v", err)
	}

	loadState()

	log.Printf("kokoni_agent starting on %s", listenAddr)

	http.HandleFunc("/", handleIndex)

	http.HandleFunc("/api/status", handleAPIStatus)
	http.HandleFunc("/api/init", handleAPIInit)
	http.HandleFunc("/api/send", handleAPISend)
	http.HandleFunc("/api/light", handleAPILight)
	http.HandleFunc("/api/logs", handleAPILogs)
	http.HandleFunc("/api/shutdown", handleAPIShutdown)

	http.HandleFunc("/api/job", handleAPIJob)
	http.HandleFunc("/api/job/upload", handleAPIJobUpload)
	http.HandleFunc("/api/job/start", handleAPIJobStart)
	http.HandleFunc("/api/job/pause", handleAPIJobPause)
	http.HandleFunc("/api/job/resume", handleAPIJobResume)
	http.HandleFunc("/api/job/cancel", handleAPIJobCancel)

	// Compatibility aliases for older UI/scripts.
	http.HandleFunc("/status", handleAPIJob)
	http.HandleFunc("/upload", handleAPIJobUpload)
	http.HandleFunc("/print", handleAPIJobStart)
	http.HandleFunc("/control", handleAPIControlCompat)

	log.Fatal(http.ListenAndServe(listenAddr, withCORS(http.DefaultServeMux)))
}

func setPrinterState(printer string, connected bool, line string) {
	statusMu.Lock()
	defer statusMu.Unlock()

	if printer != "" {
		printerState = printer
	}
	uartConnected = connected
	if line != "" {
		lastMCULine = line
	}
}

func snapshotStatus() Status {
	statusMu.Lock()
	printer := printerState
	connected := uartConnected
	last := lastMCULine
	statusMu.Unlock()

	return Status{
		Agent:         "running",
		State:         "idle",
		Printer:       printer,
		UARTConnected: connected,
		LastMCULine:   last,
		Version:       version,
		GoVersion:     runtime.Version(),
		UptimeSec:     int64(time.Since(startedAt).Seconds()),
		StartedAt:     startedAt.UTC().Format(time.RFC3339),
		StatePath:     statePath,
		LogPath:       logPath,
		Job:           snapshotJob(),
	}
}

func snapshotJob() JobStatus {
	jobMu.Lock()
	defer jobMu.Unlock()

	j := job
	if j.TotalLines > 0 {
		j.ProgressPct = int((int64(j.CurrentLine) * 100) / int64(j.TotalLines))
	}
	return j
}

func loadState() {
	b, err := os.ReadFile(statePath)
	if err != nil {
		return
	}

	var st Status
	if err := json.Unmarshal(b, &st); err != nil {
		log.Printf("load state failed: %v", err)
		return
	}

	if st.Job.Path != "" {
		jobMu.Lock()
		job = st.Job

		// Never resume automatically after process restart.
		if job.State == "printing" || job.State == "paused" || job.State == "pausing" {
			job.State = "interrupted"
			job.Error = "agent restarted while job was active"
			job.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		}

		jobMu.Unlock()
		log.Printf("[JOB] restored state: state=%s line=%d/%d file=%s", job.State, job.CurrentLine, job.TotalLines, job.FileName)
	}
}

func saveState() {
	tmp := snapshotStatus()
	b, err := json.MarshalIndent(tmp, "", "  ")
	if err != nil {
		log.Printf("marshal state failed: %v", err)
		return
	}
	if err := os.WriteFile(statePath, b, 0666); err != nil {
		log.Printf("write state failed: %v", err)
	}
}

func initUART() error {
	uartMu.Lock()

	if uart != nil {
		uartMu.Unlock()
		setPrinterState("ready", true, lastMCULine)
		return nil
	}

	setPrinterState("opening_uart", false, lastMCULine)

	p, err := serial.NewUARTController(uartDev, uartBaud)
	if err != nil {
		uartMu.Unlock()
		setPrinterState("uart_error", false, err.Error())
		return err
	}

	uart = p
	uartMu.Unlock()

	setPrinterState("connected", true, lastMCULine)

	go readMCULoop(p)

	drainOKChan()

	if err := sendLineWaitOK("M355 S255"); err != nil {
		setPrinterState("init_command_error", true, err.Error())
		return err
	}

	setPrinterState("ready", true, lastMCULine)
	return nil
}

func getUART() (*serial.UARTController, error) {
	uartMu.Lock()
	p := uart
	uartMu.Unlock()

	if p != nil {
		return p, nil
	}
	if err := initUART(); err != nil {
		return nil, err
	}

	uartMu.Lock()
	defer uartMu.Unlock()
	return uart, nil
}

func readMCULoop(p *serial.UARTController) {
	scanner := bufio.NewScanner(p)
	scanner.Buffer(make([]byte, 1024), maxScannerBufSize)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		log.Printf("MCU -> %s", line)
		setPrinterState("", true, line)

		if isReadyResponse(line) {
			select {
			case okChan <- struct{}{}:
			default:
			}
		}
	}

	if err := scanner.Err(); err != nil {
		log.Printf("UART scanner error: %v", err)
		setPrinterState("uart_read_error", false, err.Error())
	}
}

func isReadyResponse(line string) bool {
	l := strings.ToLower(strings.TrimSpace(line))
	return l == "ok" || strings.HasPrefix(l, "ok ") || strings.HasPrefix(l, "ok:") || strings.HasPrefix(l, "k t:")
}

func sendLineWaitOK(line string) error {
	p, err := getUART()
	if err != nil {
		return err
	}

	line = strings.TrimSpace(line)
	if line == "" {
		return nil
	}

	commandMu.Lock()
	defer commandMu.Unlock()

	drainOKChan()

	log.Printf("PC -> %s", line)
	if err := p.SendRaw(line); err != nil {
		return err
	}

	return waitForOK(line)
}

func sendLineNoWait(line string) error {
	p, err := getUART()
	if err != nil {
		return err
	}

	line = strings.TrimSpace(line)
	if line == "" {
		return nil
	}

	commandMu.Lock()
	defer commandMu.Unlock()

	log.Printf("PC -> %s", line)
	return p.SendRaw(line)
}

func sendLinesWaitOK(lines string) error {
	for _, line := range strings.Split(lines, "\n") {
		line = normalizeGCodeLine(line)
		if line == "" {
			continue
		}
		if err := sendLineWaitOK(line); err != nil {
			return err
		}
		time.Sleep(lineDelay)
	}
	return nil
}

func sendLinesNoWait(lines string) {
	for _, line := range strings.Split(lines, "\n") {
		line = normalizeGCodeLine(line)
		if line == "" {
			continue
		}
		if err := sendLineNoWait(line); err != nil {
			log.Printf("send no-wait failed: %v", err)
			return
		}
		time.Sleep(lineDelay)
	}
}

func waitForOK(line string) error {
	timeout := defaultOKTimeout
	upper := strings.ToUpper(strings.TrimSpace(line))
	if strings.HasPrefix(upper, "M109") || strings.HasPrefix(upper, "M190") {
		timeout = heatingOKTimeout
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	for {
		select {
		case <-okChan:
			return nil
		case <-timer.C:
			return fmt.Errorf("timeout waiting for ok: %s", line)
		}
	}
}

func drainOKChan() {
	for {
		select {
		case <-okChan:
		default:
			return
		}
	}
}

func normalizeGCodeLine(line string) string {
	line = strings.TrimSpace(line)
	if line == "" {
		return ""
	}
	if strings.HasPrefix(line, ";") || strings.HasPrefix(line, "%") {
		return ""
	}
	if idx := strings.Index(line, ";"); idx >= 0 {
		line = strings.TrimSpace(line[:idx])
	}
	if line == "" {
		return ""
	}
	for _, r := range line {
		if r > 127 {
			return ""
		}
	}
	return line
}

func countGCodeLines(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024), maxScannerBufSize)

	count := 0
	for scanner.Scan() {
		if normalizeGCodeLine(scanner.Text()) != "" {
			count++
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, err
	}
	return count, nil
}

func handleAPIStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, snapshotStatus())
}

func handleAPIInit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}

	if err := initUART(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, map[string]interface{}{
		"ok":             true,
		"printer":        printerState,
		"uart_connected": true,
	})
}

func handleAPISend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}

	j := snapshotJob()
	if j.State == "printing" || j.State == "paused" || j.State == "pausing" {
		http.Error(w, "manual send is disabled while job is active", http.StatusConflict)
		return
	}

	cmd := strings.TrimSpace(r.URL.Query().Get("cmd"))
	if cmd == "" {
		http.Error(w, "missing cmd", http.StatusBadRequest)
		return
	}

	if err := sendLinesWaitOK(cmd); err != nil {
		http.Error(w, err.Error(), http.StatusGatewayTimeout)
		return
	}

	writeJSON(w, map[string]interface{}{
		"ok":  true,
		"cmd": cmd,
	})
}

func handleAPILogs(w http.ResponseWriter, r *http.Request) {
	b, err := os.ReadFile(logPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	linesParam := strings.TrimSpace(r.URL.Query().Get("lines"))
	if linesParam != "" {
		n, err := strconv.Atoi(linesParam)
		if err != nil || n <= 0 {
			http.Error(w, "invalid lines", http.StatusBadRequest)
			return
		}
		if n > 1000 {
			n = 1000
		}

		parts := strings.Split(string(b), "\n")
		if len(parts) > n {
			parts = parts[len(parts)-n:]
		}
		b = []byte(strings.Join(parts, "\n"))
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write(b)
}

func handleAPIShutdown(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]interface{}{"ok": true})
	go func() {
		time.Sleep(300 * time.Millisecond)
		os.Exit(0)
	}()
}

func handleAPIJob(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, snapshotJob())
}

func analyzeGCodeSummary(path string) GCodeSummary {
	var summary GCodeSummary

	f, err := os.Open(path)
	if err != nil {
		summary.Warnings = append(summary.Warnings, "failed to open gcode for analysis: "+err.Error())
		return summary
	}
	defer f.Close()

	nozzleSeen := map[float64]bool{}
	bedSeen := map[float64]bool{}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024), maxScannerBufSize)

	for scanner.Scan() {
		raw := strings.TrimSpace(scanner.Text())
		if raw == "" {
			continue
		}

		if strings.HasPrefix(raw, ";") {
			parseGCodeComment(raw, &summary)
			continue
		}

		line := normalizeGCodeLine(raw)
		if line == "" {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}

		cmd := strings.ToUpper(fields[0])

		switch cmd {
		case "G0", "G1":
			updateBoundsFromFields(fields, &summary)
		case "G28":
			summary.HasHome = true
		case "M104":
			summary.HasM104 = true
			if v, ok := gcodeParam(fields, "S"); ok && !nozzleSeen[v] {
				nozzleSeen[v] = true
				summary.NozzleTemps = append(summary.NozzleTemps, v)
			}
		case "M109":
			summary.HasM109 = true
			if v, ok := gcodeParam(fields, "S"); ok && !nozzleSeen[v] {
				nozzleSeen[v] = true
				summary.NozzleTemps = append(summary.NozzleTemps, v)
			}
		case "M140":
			summary.HasM140 = true
			if v, ok := gcodeParam(fields, "S"); ok && !bedSeen[v] {
				bedSeen[v] = true
				summary.BedTemps = append(summary.BedTemps, v)
			}
		case "M106":
			summary.HasM106 = true
		}
	}

	if err := scanner.Err(); err != nil {
		summary.Warnings = append(summary.Warnings, "scanner error: "+err.Error())
	}

	if summary.HasM140 {
		summary.Warnings = append(summary.Warnings, "M140 is present; this MCU may report it as unknown")
	}
	if summary.HasM106 {
		summary.Warnings = append(summary.Warnings, "M106 is present; this MCU may report it as unknown")
	}
	if !summary.HasHome {
		summary.Warnings = append(summary.Warnings, "G28 homing command was not found")
	}
	if !summary.HasM109 {
		summary.Warnings = append(summary.Warnings, "M109 heat-and-wait command was not found")
	}

	return summary
}

func parseGCodeComment(line string, summary *GCodeSummary) {
	upper := strings.ToUpper(line)

	if strings.HasPrefix(upper, ";TIME:") {
		v := strings.TrimSpace(line[len(";TIME:"):])
		if n, err := strconv.Atoi(v); err == nil {
			summary.EstimatedTimeSec = n
		}
		return
	}

	if strings.HasPrefix(upper, ";LAYER_COUNT:") {
		v := strings.TrimSpace(line[len(";LAYER_COUNT:"):])
		if n, err := strconv.Atoi(v); err == nil {
			summary.LayerCount = n
		}
		return
	}

	if strings.HasPrefix(upper, ";FILAMENT USED:") {
		v := strings.TrimSpace(line[len(";Filament used:"):])
		v = strings.TrimSuffix(v, "m")
		v = strings.TrimSuffix(v, "M")
		if meters, err := strconv.ParseFloat(v, 64); err == nil {
			summary.FilamentMM = meters * 1000
		}
		return
	}
}

func updateBoundsFromFields(fields []string, summary *GCodeSummary) {
	if v, ok := gcodeParam(fields, "X"); ok {
		updateMinMax(v, &summary.MinX, &summary.MaxX, &summary.HasBounds)
	}
	if v, ok := gcodeParam(fields, "Y"); ok {
		updateMinMax(v, &summary.MinY, &summary.MaxY, &summary.HasBounds)
	}
	if v, ok := gcodeParam(fields, "Z"); ok {
		updateMinMax(v, &summary.MinZ, &summary.MaxZ, &summary.HasBounds)
	}
	if v, ok := gcodeParam(fields, "E"); ok {
		updateMinMax(v, &summary.MinE, &summary.MaxE, &summary.HasBounds)
	}
}

func updateMinMax(v float64, min *float64, max *float64, initialized *bool) {
	if !*initialized {
		*min = v
		*max = v
		*initialized = true
		return
	}
	if v < *min {
		*min = v
	}
	if v > *max {
		*max = v
	}
}

func gcodeParam(fields []string, key string) (float64, bool) {
	key = strings.ToUpper(key)

	for _, f := range fields[1:] {
		f = strings.TrimSpace(f)
		if len(f) < 2 {
			continue
		}
		if strings.ToUpper(f[:1]) != key {
			continue
		}
		v, err := strconv.ParseFloat(f[1:], 64)
		if err != nil {
			continue
		}
		return v, true
	}

	return 0, false
}

func handleAPIJobUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}

	j := snapshotJob()
	if j.State == "printing" || j.State == "paused" || j.State == "pausing" {
		http.Error(w, "cannot upload while job is active", http.StatusConflict)
		return
	}

	if err := r.ParseMultipartForm(256 << 20); err != nil {
		http.Error(w, "invalid multipart upload", http.StatusBadRequest)
		return
	}

	file, header, err := r.FormFile("gcode")
	if err != nil {
		http.Error(w, "missing multipart field: gcode", http.StatusBadRequest)
		return
	}
	defer file.Close()

	name := filepath.Base(header.Filename)
	ext := strings.ToLower(filepath.Ext(name))
	if ext != ".gcode" {
		http.Error(w, "only .gcode files are allowed", http.StatusBadRequest)
		return
	}

	if err := os.MkdirAll(jobsDir, 0777); err != nil {
		http.Error(w, "failed to prepare jobs dir", http.StatusInternalServerError)
		return
	}

	tmpPath := currentGCode + ".tmp"
	out, err := os.Create(tmpPath)
	if err != nil {
		http.Error(w, "failed to create gcode file", http.StatusInternalServerError)
		return
	}

	if _, err := io.Copy(out, file); err != nil {
		_ = out.Close()
		http.Error(w, "failed to save gcode", http.StatusInternalServerError)
		return
	}
	if err := out.Close(); err != nil {
		http.Error(w, "failed to close gcode file", http.StatusInternalServerError)
		return
	}

	if err := os.Rename(tmpPath, currentGCode); err != nil {
		http.Error(w, "failed to finalize gcode file", http.StatusInternalServerError)
		return
	}
	_ = os.Chmod(currentGCode, 0666)

	total, err := countGCodeLines(currentGCode)
	if err != nil {
		http.Error(w, "failed to count gcode lines: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if total <= 0 {
		http.Error(w, "gcode has no printable lines", http.StatusBadRequest)
		return
	}

	summary := analyzeGCodeSummary(currentGCode)

	now := time.Now().UTC().Format(time.RFC3339)

	jobMu.Lock()
	job = JobStatus{
		State:       "uploaded",
		FileName:    name,
		Path:        currentGCode,
		TotalLines:  total,
		CurrentLine: 0,
		UpdatedAt:   now,
		Summary:     summary,
	}
	jobMu.Unlock()

	saveState()

	writeJSON(w, map[string]interface{}{
		"ok":          true,
		"file_name":   name,
		"path":        currentGCode,
		"total_lines": total,
		"summary":     summary,
	})
}

func handleAPIJobStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodGet {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}

	if err := initUART(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if _, err := os.Stat(currentGCode); err != nil {
		http.Error(w, "no uploaded gcode", http.StatusNotFound)
		return
	}

	jobMu.Lock()
	if job.State == "printing" || job.State == "paused" || job.State == "pausing" {
		jobMu.Unlock()
		http.Error(w, "job already active", http.StatusConflict)
		return
	}

	if job.TotalLines <= 0 {
		total, err := countGCodeLines(currentGCode)
		if err != nil {
			jobMu.Unlock()
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		job.TotalLines = total
	}
	if job.TotalLines <= 0 {
		jobMu.Unlock()
		http.Error(w, "gcode has no printable lines", http.StatusBadRequest)
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)
	job.State = "printing"
	job.CurrentLine = 0
	job.Cancel = false
	job.Error = ""
	job.PauseLifted = false
	job.StartedAt = now
	job.UpdatedAt = now
	job.Path = currentGCode
	jobMu.Unlock()

	saveState()

	go runCurrentJob()

	writeJSON(w, map[string]interface{}{
		"ok":  true,
		"job": snapshotJob(),
	})
}

func applyPauseLift() error {
	log.Printf("[JOB] pause lift: Z +%dmm", pauseLiftMM)
	return sendLinesWaitOK(fmt.Sprintf("G91\nG1 Z%d F600\nG90", pauseLiftMM))
}

func undoPauseLift() error {
	log.Printf("[JOB] resume lift: Z -%dmm", pauseLiftMM)
	return sendLinesWaitOK(fmt.Sprintf("G91\nG1 Z-%d F600\nG90", pauseLiftMM))
}

func handleAPIJobPause(w http.ResponseWriter, r *http.Request) {
	jobMu.Lock()
	if job.State != "printing" {
		jobMu.Unlock()
		http.Error(w, "job is not printing", http.StatusConflict)
		return
	}
	job.State = "pausing"
	job.PauseLifted = false
	job.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	jobMu.Unlock()

	saveState()
	writeJSON(w, map[string]interface{}{"ok": true, "job": snapshotJob()})
}

func handleAPIJobResume(w http.ResponseWriter, r *http.Request) {
	jobMu.Lock()
	if job.State != "paused" {
		jobMu.Unlock()
		http.Error(w, "job is not paused", http.StatusConflict)
		return
	}
	wasLifted := job.PauseLifted
	jobMu.Unlock()

	if wasLifted {
		if err := undoPauseLift(); err != nil {
			http.Error(w, err.Error(), http.StatusGatewayTimeout)
			return
		}
	}

	jobMu.Lock()
	job.State = "printing"
	job.PauseLifted = false
	job.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	jobMu.Unlock()

	saveState()
	writeJSON(w, map[string]interface{}{"ok": true, "job": snapshotJob()})
}

func handleAPIJobCancel(w http.ResponseWriter, r *http.Request) {
	jobMu.Lock()
	if job.State != "printing" && job.State != "paused" && job.State != "pausing" {
		jobMu.Unlock()
		http.Error(w, "job is not active", http.StatusConflict)
		return
	}
	job.Cancel = true
	job.State = "cancelled"
	job.PauseLifted = false
	job.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	jobMu.Unlock()

	saveState()
	writeJSON(w, map[string]interface{}{"ok": true, "job": snapshotJob()})
}

func handleAPIControlCompat(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Query().Get("action") {
	case "pause":
		handleAPIJobPause(w, r)
	case "resume":
		handleAPIJobResume(w, r)
	case "stop", "cancel", "abort":
		handleAPIJobCancel(w, r)
	default:
		http.Error(w, "unknown action", http.StatusBadRequest)
	}
}

func handleAPILight(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}

	raw := strings.TrimSpace(r.URL.Query().Get("value"))
	if raw == "" {
		http.Error(w, "missing value", http.StatusBadRequest)
		return
	}

	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 || value > 255 {
		http.Error(w, "value must be 0..255", http.StatusBadRequest)
		return
	}

	j := snapshotJob()
	if j.State == "printing" || j.State == "paused" || j.State == "pausing" {
		setPendingLight(value)
		writeJSON(w, map[string]interface{}{
			"ok":     true,
			"mode":   "queued",
			"value":  value,
			"reason": "job is active; light command will be inserted at a safe line boundary",
		})
		return
	}

	cmd := fmt.Sprintf("M355 S%d", value)
	if err := sendLineWaitOK(cmd); err != nil {
		http.Error(w, err.Error(), http.StatusGatewayTimeout)
		return
	}

	writeJSON(w, map[string]interface{}{
		"ok":    true,
		"mode":  "sent",
		"value": value,
		"cmd":   cmd,
	})
}

func setPendingLight(value int) {
	pendingLightMu.Lock()
	defer pendingLightMu.Unlock()

	v := value
	pendingLightValue = &v
}

func takePendingLight() (int, bool) {
	pendingLightMu.Lock()
	defer pendingLightMu.Unlock()

	if pendingLightValue == nil {
		return 0, false
	}

	value := *pendingLightValue
	pendingLightValue = nil
	return value, true
}

func applyPendingLightIfAny() error {
	value, ok := takePendingLight()
	if !ok {
		return nil
	}

	cmd := fmt.Sprintf("M355 S%d", value)
	log.Printf("[JOB] apply pending light -> %s", cmd)
	return sendLineWaitOK(cmd)
}

func runCurrentJob() {
	log.Printf("[JOB] start: %s", currentGCode)

	file, err := os.Open(currentGCode)
	if err != nil {
		failJob(err)
		return
	}
	defer file.Close()

	drainOKChan()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1024), maxScannerBufSize)

	for scanner.Scan() {
		line := normalizeGCodeLine(scanner.Text())
		if line == "" {
			continue
		}
		for {
			j := snapshotJob()
			if j.Cancel || j.State == "cancelled" {
				log.Printf("[JOB] cancelled")
				sendLinesNoWait("M104 S0")
				return
			}

			if j.State == "pausing" {
				if err := applyPauseLift(); err != nil {
					failJob(err)
					return
				}

				jobMu.Lock()
				if job.State == "pausing" {
					job.State = "paused"
					job.PauseLifted = true
					job.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
				}
				jobMu.Unlock()
				saveState()
				continue
			}

			if j.State != "paused" {
				break
			}
			time.Sleep(300 * time.Millisecond)
		}

		if err := applyPendingLightIfAny(); err != nil {
			failJob(err)
			return
		}

		jobMu.Lock()
		job.CurrentLine++
		job.LastCommand = line
		job.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		current := job.CurrentLine
		total := job.TotalLines
		jobMu.Unlock()

		if current == 1 || current%20 == 0 {
			saveState()
		}

		log.Printf("[JOB] %d/%d -> %s", current, total, line)

		if err := sendLineWaitOK(line); err != nil {
			failJob(err)
			return
		}

		time.Sleep(lineDelay)
	}

	if err := scanner.Err(); err != nil {
		failJob(err)
		return
	}

	jobMu.Lock()
	if job.State != "cancelled" {
		job.State = "done"
		job.Cancel = false
		job.PauseLifted = false
		job.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	jobMu.Unlock()

	saveState()
	log.Printf("[JOB] done")
}

func failJob(err error) {
	log.Printf("[JOB] error: %v", err)

	jobMu.Lock()
	job.State = "error"
	job.Error = err.Error()
	job.PauseLifted = false
	job.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	jobMu.Unlock()

	saveState()
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}

func handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, `<!doctype html>
<html>
<head>
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>KOKONI Agent</title>
  <style>
    body{font-family:sans-serif;background:#111;color:#eee;margin:0;padding:16px;max-width:720px}
    button{padding:10px 14px;margin:4px;border:0;border-radius:6px;background:#333;color:#fff}
    input{margin:8px 0}
    pre{background:#1d1d1d;padding:12px;overflow:auto}
    .card{background:#1a1a1a;padding:14px;margin:12px 0;border-radius:8px}
  </style>
</head>
<body>
  <h2>KOKONI Agent</h2>

  <div class="card">
    <button onclick="api('/api/init')">Init UART</button>
    <button onclick="send('M355 S0')">Light OFF</button>
    <button onclick="send('M355 S255')">Light ON</button>
  </div>

  <div class="card">
    <input type="file" id="gcode">
    <button onclick="upload()">Upload</button>
    <button onclick="api('/api/job/start')">Start</button>
    <button onclick="api('/api/job/pause')">Pause</button>
    <button onclick="api('/api/job/resume')">Resume</button>
    <button onclick="api('/api/job/cancel')">Cancel</button>
  </div>

  <pre id="out"></pre>

<script>
async function api(path){
  const r = await fetch(path, {method:'POST'});
  document.getElementById('out').textContent = await r.text();
  refresh();
}
async function send(cmd){
  const r = await fetch('/api/send?cmd='+encodeURIComponent(cmd), {method:'POST'});
  document.getElementById('out').textContent = await r.text();
  refresh();
}
async function upload(){
  const f = document.getElementById('gcode').files[0];
  if(!f){ alert('select gcode'); return; }
  const fd = new FormData();
  fd.append('gcode', f);
  const r = await fetch('/api/job/upload', {method:'POST', body:fd});
  document.getElementById('out').textContent = await r.text();
  refresh();
}
async function refresh(){
  const r = await fetch('/api/status');
  document.getElementById('out').textContent = JSON.stringify(await r.json(), null, 2);
}
setInterval(refresh, 1000);
refresh();
</script>
</body>
</html>`)
}
