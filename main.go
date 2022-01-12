/*    
	Copyright 2021 Google LLC
 
    Licensed under the Apache License, Version 2.0 (the "License");
    you may not use this file except in compliance with the License.
    You may obtain a copy of the License at
 
        http://www.apache.org/licenses/LICENSE-2.0
 
    Unless required by applicable law or agreed to in writing, software
    distributed under the License is distributed on an "AS IS" BASIS,
    WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
    See the License for the specific language governing permissions and
    limitations under the License.
*/
package main

import (
	"bufio"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"syscall"
	"time"
	"io/ioutil"
	"encoding/json"
	backoff "github.com/cenkalti/backoff/v4"
)

var POLL_TIME time.Duration = 5 * time.Second
var MAX_POLL_TIME time.Duration = 300 * time.Second

type Command struct {
	Name string
	Args []string

	ShowOutput       bool
	CanFail          bool
	AllowedExitCodes []int
	Request			 *http.Request
	Response         *http.ResponseWriter
	Flusher          *http.Flusher

	StdoutLogger log.Logger
	StderrLogger log.Logger
}

type PubSubMessage struct {
	Message struct {
		Data []byte `json:"data,omitempty"`
		ID   string `json:"id"`
	} `json:"message"`
	Subscription string `json:"subscription"`
}

func main() {
	log.Print("Starting Cloud Run function...")
	http.HandleFunc("/", handler)

	// Determine port for HTTP service.
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
		log.Printf("Defaulting to port %s", port)
	}

	// Start HTTP server.
	log.Printf("Listening on port %s", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatal(err)
	}
}

func NewCommand(request *http.Request, response *http.ResponseWriter, flusher *http.Flusher, name string, args ...string) *Command {
	stdoutLogger := *log.New(os.Stdout, fmt.Sprintf("[%s] ", name), log.Ldate|log.Ltime)
	stderrLogger := *log.New(os.Stderr, fmt.Sprintf("[%s] ", name), log.Ldate|log.Ltime)
	return &Command{
		Name:             name,
		Args:             args,
		ShowOutput:       true,
		CanFail:          false,
		AllowedExitCodes: []int{0},
		Request: request,
		Response:         response,
		Flusher:          flusher,
		StdoutLogger:     stdoutLogger,
		StderrLogger:     stderrLogger,
	}
}

func (c Command) writeProgress(message string) {
	c.StderrLogger.Println(message)
	fmt.Fprintln(*c.Response, message)
	(*c.Flusher).Flush()
}

func (c Command) Run() error {
	c.writeProgress(fmt.Sprintf("Running command: %s", c.Name))
	c.StdoutLogger.Printf("Running as: %s %+q", c.Name, c.Args)

	// Build command
	cmd := exec.CommandContext(c.Request.Context(), c.Name, c.Args...)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("error getting stdout pipe: %w", err)
	}
	stdoutBuf := bufio.NewScanner(stdout)

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("error getting stderr pipe: %w", err)
	}
	stderrBuf := bufio.NewScanner(stderr)

	startTime := time.Now()
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("error starting command: %w", err)
	}

	done := make(chan error)
	output := make(chan string)

	// Wait for actual command to complete
	go func() { 
		done <- cmd.Wait() 
	}()

	// Read stdout and stderr and relay output via channel
	go func() {
		for stdoutBuf.Scan() {
			text := stdoutBuf.Text()
			output <- text
		}
	}()
	go func() {
		for stderrBuf.Scan() {
			text := stderrBuf.Text()
			output <- text
		}
	}()

	b := backoff.NewExponentialBackOff()
	bctx := backoff.WithContext(b, c.Request.Context())
	b.InitialInterval = POLL_TIME
	b.MaxInterval = MAX_POLL_TIME
	b.Stop = backoff.Stop

	// This will set the maximum duration the command can run
	b.MaxElapsedTime = 60 * time.Minute

	pollTimer := backoff.NewTicker(bctx)
	lastIntervalTime := time.Now()
	processTerminated := false
	for {
		select {
		case line := <-output:
			if c.ShowOutput {
				c.writeProgress(line)
			} else {
				c.StderrLogger.Println(line)
			}
		case tick := <-pollTimer.C:
			intervalTime := time.Now()
			if tick.Year() == 1 {
				if !processTerminated {
					pollTimer.Stop()
					if err := cmd.Process.Kill(); err != nil {
						return fmt.Errorf("Failed to terminate command: %w", err)
					}
					c.writeProgress(fmt.Sprintf("Command timed out in %s: %s", b.MaxElapsedTime.Truncate(time.Minute).String(), c.Name))
					processTerminated = true
				}
			} else {
				if intervalTime.Sub(lastIntervalTime) > time.Second {
					c.writeProgress(fmt.Sprintf("[Still waiting for command to complete: %s --- %s]", c.Name, intervalTime.Sub(startTime).Truncate(time.Second).String()))
					lastIntervalTime = time.Now()
				}
			}
		case err := <-done:
			pollTimer.Stop()
			endTime := time.Now()
			commandDuration := endTime.Sub(startTime).Truncate(time.Second).String()
			if err != nil {
				if c.CanFail {
					c.StdoutLogger.Printf("Warning, command failed (ignoring error) in %s: %v", commandDuration, err)
					return nil
				}
				if exiterr, ok := err.(*exec.ExitError); ok {
					if status, ok := exiterr.Sys().(syscall.WaitStatus); ok {
						for _, exitcode := range c.AllowedExitCodes {
							if status.ExitStatus() == exitcode {
								c.StdoutLogger.Printf("Command completed with allowed status code in %s: %d", commandDuration, status.ExitStatus())
								return nil
							}
						}
						return fmt.Errorf("Command exited with status code in %s: %d", commandDuration, status.ExitStatus())
					}
				}
				return fmt.Errorf("Command failed in %s: %w", commandDuration, err)
			} else {
				c.writeProgress(fmt.Sprintf("Command completed in %s: %s", commandDuration, c.Name))
				return nil
			}
		}
	}
}

func handler(w http.ResponseWriter, r *http.Request) {
	if len(os.Args) == 1 {
		log.Fatalf("No command to run set!")
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	go func(done <-chan struct{}) {
        <-done
        log.Println("Client closed connection, command terminating.")
    }(r.Context().Done())


	var m PubSubMessage
	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		log.Printf("Failed to read request body: %v", err)
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}
	if len(body) > 0 {
		if err := json.Unmarshal(body, &m); err != nil {
			log.Printf("Failed to parse JSON body: %v", err)
			http.Error(w, "Bad Request", http.StatusBadRequest)
			return
		}
	} else {
		log.Println("Not a Pub/Sub invocation (no request body).")
	}

	// You can unmarshal m.Message.Data here to leverage Pub/Sub message contents
	// as arguments

	w.Header().Set("Transfer-Encoding", "chunked")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	var commandArgs []string
	if len(os.Args) > 2 {
		commandArgs = os.Args[2:len(os.Args)]
	}
	command := NewCommand(r, &w, &flusher, os.Args[1], commandArgs...)
	err = command.Run()
	if err != nil {
		log.Fatal(err)
	}
}