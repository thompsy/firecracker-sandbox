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
	"github.com/thompsy/firecracker-sandbox/flow"
)

type Server struct {
	reg  *registry
	flow *flow.Monitor
}

func NewServer() *Server {
	return &Server{
		reg: newRegistry(),
	}
}

// Reconcile builds the registry from on-disk and running process state in case of crash.
func (s *Server) Reconcile() error {
	f, err := flow.Load()
	if err != nil {
		return err
	}
	s.flow = f

	states, err := firevm.AllStates()
	if err != nil {
		return err
	}

	for _, state := range states {
		vm := NewVM(state)
		if vm.Alive() {
			s.reg.Add(vm)
			err = s.flow.Attach(vm.State.Tap)
			if err != nil {
				slog.Warn("failed to attach flow monitor", "id", state.ID, "err", err)
			}

			slog.Info("recovered vm", "id", state.ID, "pid", state.Pid)
			continue
		}
		slog.Info("reaping dead vm", "id", state.ID, "pid", state.Pid)
		s.reap(state.ID)

	}

	return nil
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
	mux.HandleFunc("GET /stats", s.handleStats)

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

	// Reject a launch for an id that's already running, so we don't clobber a live VM's
	// tap/socket.
	//
	//NOTE: this check and the Add below aren't atomic, so two truly-concurrent launches of the same id
	//could still race. A reserve-under-lock in the registry would close that. Fine for now.
	if _, err := s.reg.Get(req.ID); err == nil {
		http.Error(w, "vm already exists", http.StatusConflict)
		return
	}

	state, err := firevm.Launch(context.Background(), req.ID, req.Cmd)
	if err != nil {
		slog.Error("launch failed", "id", req.ID, "err", err)
		http.Error(w, "failed to launch VM", http.StatusInternalServerError)
		return
	}

	err = s.flow.Attach(firevm.TapName(state.ID))
	if err != nil {
		slog.Warn("failed to attach flow monitor", "id", req.ID, "err", err)
	}

	err = firevm.SaveState(state)
	if err != nil {
		slog.Error("failed to save VM state", "id", req.ID, "err", err)
		http.Error(w, "failed to save VM state", http.StatusInternalServerError)
		return
	}

	s.reg.Add(NewVM(state))
	slog.Info("launched vm", "id", state.ID, "pid", state.Pid, "ip", state.GuestIP, "cmd", req.Cmd)

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

// handleStats returns the current eBPF flow counters, each attributed to the VM
// that owns the tap the flow was seen on.
func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	samples, err := s.flow.Stats()
	if err != nil {
		slog.Error("failed to read flow stats", "err", err)
		http.Error(w, "failed to read flow stats", http.StatusInternalServerError)
		return
	}

	// ifindex -> VM, so each flow can be attributed by the tap it was seen on.
	byIfindex := make(map[uint32]*firevm.State)
	for _, st := range s.reg.List() {
		byIfindex[uint32(st.Ifindex)] = st
	}

	stats := make([]api.FlowStat, 0, len(samples))
	for _, sample := range samples {
		vm, ok := byIfindex[sample.Ifindex]
		if !ok {
			continue // a flow for a tap we no longer track — skip
		}
		dir := "in"
		if sample.Egress {
			dir = "out"
		}
		stats = append(stats, api.FlowStat{
			VMID:    vm.ID,
			VM:      vm.Name,
			Dir:     dir,
			SrcIP:   flow.IPv4(sample.SrcAddr).String(),
			SrcPort: flow.Port(sample.SrcPort),
			DstIP:   flow.IPv4(sample.DstAddr).String(),
			DstPort: flow.Port(sample.DstPort),
			Proto:   flow.Proto(sample.Proto),
			Packets: sample.Packets,
			Bytes:   sample.Bytes,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(stats)
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

	s.reap(id)

	s.reg.Remove(id)
	slog.Info("stopped vm", "id", id, "pid", state.Pid)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) reap(id int) {
	err := s.flow.Detach(firevm.TapName(id))
	if err != nil {
		slog.Error("failed to detach flow monitor", "id", id, "err", err)
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
}
