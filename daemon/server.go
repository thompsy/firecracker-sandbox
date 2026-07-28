package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"syscall"

	"github.com/thompsy/firecracker-sandbox/api"
	"github.com/thompsy/firecracker-sandbox/firevm"
)

type Server struct {
	reg *registry
}

func NewServer() *Server {
	return &Server{
		reg: newRegistry(),
	}
}

func (s *Server) Run() error {
	// Remove any existing socket
	err := os.Remove(firevm.DaemonSock())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}

	// Open a unix socket
	ln, err := net.Listen("unix", firevm.DaemonSock())
	if err != nil {
		return err
	}

	// Ensure the socket is only accessible by root
	err = os.Chmod(firevm.DaemonSock(), 0o600)
	if err != nil {
		return err
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /vms", s.handleLaunch)
	mux.HandleFunc("GET /vms", s.handleList)
	mux.HandleFunc("DELETE /vms/{id}", s.handleStop)

	srv := &http.Server{Handler: mux}
	return srv.Serve(ln)
}

func (s *Server) handleLaunch(w http.ResponseWriter, r *http.Request) {
	var req api.LaunchRequest
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	// Reject a launch for an id that's already running, so we don't clobber a
	// live VM's tap/socket. NOTE: this check and the Add below aren't atomic, so
	// two truly-concurrent launches of the same id could still race — a
	// reserve-under-lock in the registry would close that. Fine for now.
	if _, err := s.reg.Get(req.ID); err == nil {
		http.Error(w, "vm already exists", http.StatusConflict)
		return
	}

	state, err := firevm.Launch(context.Background(), req.ID)
	if err != nil {
		slog.Error("launch failed", "id", req.ID, "err", err)
		http.Error(w, "failed to launch VM", http.StatusInternalServerError)
		return
	}

	err = firevm.SaveState(state)
	if err != nil {
		slog.Error("failed to save VM state", "id", req.ID, "err", err)
		http.Error(w, "failed to save VM state", http.StatusInternalServerError)
		return
	}

	s.reg.Add(&VM{State: state})
	slog.Info("launched vm", "id", state.ID, "pid", state.Pid, "ip", state.GuestIP)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(state)
}

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	states := s.reg.List()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(states)
}

func (s *Server) handleStop(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	state, err := s.reg.Get(id)
	if err != nil {
		http.Error(w, "no such VM", http.StatusNotFound)
		return
	}

	err = syscall.Kill(state.Pid, syscall.SIGTERM)
	if err != nil && !errors.Is(err, syscall.ESRCH) {
		slog.Error("failed to signal VM", "id", id, "pid", state.Pid, "err", err)
		http.Error(w, "failed to kill VM", http.StatusInternalServerError)
		return
	}

	err = firevm.DelTap(id)
	if err != nil {
		slog.Error("failed to remove TAP", "id", id, "err", err)
	}

	err = os.Remove(firevm.Socket(id))
	if err != nil {
		slog.Error("failed to remove socket", "id", id, "err", err)
	}

	err = firevm.RemoveState(id)
	if err != nil {
		slog.Error("failed to remove state", "id", id, "err", err)
	}

	s.reg.Remove(id)
	slog.Info("stopped vm", "id", id, "pid", state.Pid)
	w.WriteHeader(http.StatusNoContent)
}
