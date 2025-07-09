package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"log"

	"github.com/coreos/go-systemd/daemon"
)

type CommandArgs struct {
	ComputerName     string `json:"computer_name"`
	ComputerPassword string `json:"computer_password"`
	DcIp             string `json:"dc_ip"`
	DcHost           string `json:"dc_host"`
	BaseDN           string `json:"baseDN"`
	ComputerGroup    string `json:"computer_group"`
	Method           string `json:"method"`
	DomainAuth       string `json:"domain_auth"`
	Option           string `json:"option"`
}

func (c *CommandArgs) ToArgs() []string {
	return []string{
		"-computer-name", "$computer_name",
		"-computer-pass", "$computer_password",
		"-dc-ip", "$domain_controller_ip",
		"-dc-host", "$domain_controller_host",
		"-baseDN", "$baseDN",
		"-computer-group", "$computer_group",
		"-method=$method",
		"$domain_auth",
		"$option",
	}
}

type CommandResponse struct {
	Output   string `json:"output,omitempty"`
	Error    string `json:"error,omitempty"`
	ExitCode int    `json:"exit_code"`
}

var cmd string = "/usr/local/pf/bin/impacket/impacket_addcomputer.py"

// Base context
var ctx context.Context

func main() {

	// Setup graceful shutdown
	rootCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle interruptions
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		cancel()
	}()

	// Notify systemd we're ready
	daemon.SdNotify(false, "READY=1")

	// Setup systemd watchdog
	go setupSystemdWatchdog(rootCtx)

	http.HandleFunc("/ntlm-join", executeHandler)
	http.HandleFunc("/health", healthHandler)

	port := "8080"
	log.Printf("Server started on port %s", port)
	log.Fatal(http.ListenAndServe("127.0.0.1:"+port, nil))
}

func executeHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not authorized", http.StatusMethodNotAllowed)
		return
	}

	var req CommandArgs
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	response := executeCommand(cmd, req.ToArgs())

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func executeCommand(command string, args []string) CommandResponse {
	var cmd *exec.Cmd

	cmd = exec.Command("sh", "-c", command)

	if len(args) > 0 {
		cmd.Args = append(cmd.Args, args...)
	}

	output, err := cmd.CombinedOutput()
	exitCode := cmd.ProcessState.ExitCode()

	response := CommandResponse{
		ExitCode: exitCode,
	}

	if err != nil {
		response.Error = err.Error()
	}

	response.Output = string(output)
	return response
}

// setupSystemdWatchdog configures systemd watchdog reporting
func setupSystemdWatchdog(ctx context.Context) {
	interval, err := daemon.SdWatchdogEnabled(false)
	if err != nil || interval == 0 {
		return
	}

	cli := &http.Client{
		Timeout: 5 * time.Second,
	}

	ticker := time.NewTicker(interval / 3)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			req, err := http.NewRequestWithContext(ctx, "GET", "http://127.0.0.1:8080/health", nil)
			if err != nil {
				log.Print(err.Error())
				continue
			}

			resp, err := cli.Do(req)
			if err != nil {
				log.Print(err.Error())
				continue
			}
			resp.Body.Close()

			daemon.SdNotify(false, "WATCHDOG=1")
		}
	}
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "{\"status\": \"ok\"}")
}
