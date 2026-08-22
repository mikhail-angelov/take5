// spike-host is the Go counterpart to test/spike/hosts/host.js — the passive instrument for
// SPIKE.md §1.1 and §1.5, rebuilt so the "dock" experiment can register a real Go binary as
// the native-messaging host instead of a Node script behind a shebang. It answers the same
// question host.js does (does the host spawn under Chrome's restricted GUI PATH, and does it
// stay alive through silence) and nothing else — the real host lives in cmd/take5.
//
// Not part of the product: built only by test/spike/run.mjs, and only for the "dock-go" run.
package main

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

func main() {
	exe, err := os.Executable()
	if err != nil {
		exe = os.Args[0]
	}
	// Mirrors host.js: LOG = join(dirname(host.js), "..", "host.log") = test/spike/host.log,
	// which holds as long as this binary is built into test/spike/hosts/ (run.mjs's job).
	logPath := filepath.Join(filepath.Dir(exe), "..", "host.log")
	logFile, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) // #nosec G302 G304 G703
	if err != nil {
		os.Exit(1)
	}
	defer logFile.Close()

	started := time.Now()
	log := func(line string) {
		dt := time.Since(started).Seconds()
		fmt.Fprintf(logFile, "[+%6.1fs] pid=%d %s\n", dt, os.Getpid(), line)
	}

	log(fmt.Sprintf("host spawned by chrome, argv=%v", os.Args[1:]))
	log(fmt.Sprintf("interpreter=%s", exe))
	log(fmt.Sprintf("PATH=%s", os.Getenv("PATH")))

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	go func() {
		s := <-sig
		log(fmt.Sprintf("signal %s", s))
		os.Exit(0)
	}()

	heartbeat := time.NewTicker(5 * time.Second)
	defer heartbeat.Stop()
	go func() {
		for range heartbeat.C {
			log("alive")
		}
	}()

	// Length-prefixed JSON: 4-byte little-endian length, then UTF-8 JSON — the same framing
	// internal/host uses for the real product.
	for {
		var lenBuf [4]byte
		if _, err := io.ReadFull(os.Stdin, lenBuf[:]); err != nil {
			break
		}
		n := binary.LittleEndian.Uint32(lenBuf[:])
		payload := make([]byte, n)
		if _, err := io.ReadFull(os.Stdin, payload); err != nil {
			break
		}
		log(fmt.Sprintf("<- %s", payload))
	}

	log("STDIN CLOSED — chrome tore the port down")
}
