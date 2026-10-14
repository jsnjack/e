package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"
)

const fakeServerEnv = "E_LSP_FAKE_SERVER"

// TestMain lets the test binary act as a scripted language server.
func TestMain(m *testing.M) {
	if mode := os.Getenv(fakeServerEnv); mode != "" {
		runFakeServer(mode)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// runFakeServer speaks just enough LSP for lifecycle tests:
//   - cooperative: answers initialize and shutdown, exits on exit.
//   - stubborn: answers initialize, then ignores shutdown and exit.
//   - silent: never answers anything.
//   - quit: answers initialize, then exits after initialized.
func runFakeServer(mode string) {
	reader := bufio.NewReader(os.Stdin)
	for {
		body, err := readFrame(reader)
		if err != nil {
			return
		}
		var message struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if err := json.Unmarshal(body, &message); err != nil {
			return
		}
		switch {
		case mode == "silent":
		case message.Method == "initialize":
			reply(message.ID)
		case message.Method == "initialized" && mode == "quit":
			return
		case message.Method == "shutdown" && mode == "cooperative":
			reply(message.ID)
		case message.Method == "exit" && mode == "cooperative":
			return
		}
	}
}

func reply(id json.RawMessage) {
	body := fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":{}}`, id)
	if _, err := fmt.Fprintf(os.Stdout, "Content-Length: %d\r\n\r\n%s", len(body), body); err != nil {
		os.Exit(1)
	}
}

func fakeClient(t *testing.T, mode string) *Client {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(fakeServerEnv, mode)
	return New(executable, nil, t.TempDir(), nil)
}

func waitExited(t *testing.T, client *Client, within time.Duration) {
	t.Helper()
	select {
	case <-client.exited:
	case <-time.After(within):
		t.Fatal("language server was not reaped")
	}
	if client.cmd.ProcessState == nil {
		t.Fatal("process has no exit state after reaping")
	}
}

func TestCloseStopsServer(t *testing.T) {
	tests := []struct {
		name   string
		mode   string
		within time.Duration
	}{
		{name: "cooperative", mode: "cooperative", within: time.Second},
		{name: "stubborn is killed", mode: "stubborn", within: 3 * time.Second},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := fakeClient(t, test.mode)
			client.timeouts.shutdown, client.timeouts.exit = 300*time.Millisecond, 300*time.Millisecond
			if err := client.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			var wg sync.WaitGroup
			for range 4 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					client.Close()
				}()
			}
			wg.Wait()
			waitExited(t, client, test.within)
			client.Close()
			if err := client.Change("file:///x", 2, nil); err == nil {
				t.Fatal("change after close succeeded")
			}
		})
	}
}

func TestServerExitIsReaped(t *testing.T) {
	client := fakeClient(t, "quit")
	if err := client.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitExited(t, client, 2*time.Second)
	client.Close()
}

func TestInitializeTimesOut(t *testing.T) {
	client := fakeClient(t, "silent")
	client.timeouts = timeouts{200 * time.Millisecond, 200 * time.Millisecond, 200 * time.Millisecond}
	err := client.Start(context.Background())
	if err == nil {
		t.Fatal("Start succeeded against a silent server")
	}
	waitExited(t, client, 2*time.Second)
}

func TestStartAfterCloseFails(t *testing.T) {
	client := fakeClient(t, "cooperative")
	client.Close()
	if err := client.Start(context.Background()); err == nil {
		t.Fatal("Start succeeded after Close")
	}
}
