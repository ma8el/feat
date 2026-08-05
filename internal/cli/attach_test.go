package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/ma8el/feat/internal/api"
)

type recordingAttacher struct {
	info   api.AttachInfo
	called bool
	err    error
}

func (r *recordingAttacher) Attach(_ context.Context, info api.AttachInfo, _ io.Reader, _, _ io.Writer) error {
	r.called = true
	r.info = info
	return r.err
}

func TestAttachCommandResolvesTheLiveTargetThroughTheDaemon(t *testing.T) {
	layout := isolate(t)
	want := api.AttachInfo{
		Socket:  layout.TmuxSocket(),
		Session: "$2",
		Window:  "@7",
		Pane:    "%11",
	}

	listener, err := net.Listen("unix", layout.Socket)
	if err != nil {
		t.Fatalf("listening on fake daemon socket: %v", err)
	}
	server := &http.Server{
		Handler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if request.Method != http.MethodPost {
				t.Errorf("method = %s, want POST", request.Method)
			}
			if request.URL.Path != "/v1/tasks/task-1/attach-info" {
				t.Errorf("path = %s, want attach-info path", request.URL.Path)
			}
			writer.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(writer).Encode(want); err != nil {
				t.Errorf("encoding attach info: %v", err)
			}
		}),
	}
	serverDone := make(chan error, 1)
	go func() { serverDone <- server.Serve(listener) }()
	t.Cleanup(func() {
		if err := server.Close(); err != nil {
			t.Errorf("closing fake daemon: %v", err)
		}
		if err := <-serverDone; err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Errorf("serving fake daemon: %v", err)
		}
	})

	attacher := &recordingAttacher{}
	var stdout, stderr bytes.Buffer
	code := execute(context.Background(), Options{
		Layout:   &layout,
		Attacher: attacher,
	}, []string{"attach", "task-1"}, &stdout, &stderr)
	if code != ExitOK {
		t.Fatalf("exit code = %d, want %d; stderr: %s", code, ExitOK, stderr.String())
	}
	if !attacher.called {
		t.Fatal("native attacher was not called")
	}
	if attacher.info != want {
		t.Errorf("attach info = %+v, want %+v", attacher.info, want)
	}
}

func TestAttachInfoValidationRequiresStableTargets(t *testing.T) {
	valid := api.AttachInfo{Socket: "/runtime/feat/tmux.sock", Session: "$2", Window: "@7", Pane: "%11"}
	if err := validateAttachInfo(valid); err != nil {
		t.Fatalf("valid info: %v", err)
	}

	for name, mutate := range map[string]func(*api.AttachInfo){
		"relative socket": func(info *api.AttachInfo) { info.Socket = "tmux.sock" },
		"session name":    func(info *api.AttachInfo) { info.Session = "project" },
		"window index":    func(info *api.AttachInfo) { info.Window = "7" },
		"pane index":      func(info *api.AttachInfo) { info.Pane = "1" },
	} {
		t.Run(name, func(t *testing.T) {
			info := valid
			mutate(&info)
			if err := validateAttachInfo(info); err == nil {
				t.Errorf("validateAttachInfo(%+v) succeeded", info)
			}
		})
	}
}

func TestNativeAttachCanLeaveAnOrdinaryTmuxSession(t *testing.T) {
	environment := []string{"PATH=/usr/bin", "TMUX=/tmp/tmux/default,1,0", "TMUX_PANE=%3", "TERM=xterm-256color"}
	filtered := outsideTmux(environment)
	joined := strings.Join(filtered, "\n")
	if strings.Contains(joined, "TMUX=") || strings.Contains(joined, "TMUX_PANE=") {
		t.Errorf("ordinary tmux identity remains in environment: %v", filtered)
	}
	for _, want := range []string{"PATH=/usr/bin", "TERM=xterm-256color"} {
		if !strings.Contains(joined, want) {
			t.Errorf("environment lost %q: %v", want, filtered)
		}
	}
}
