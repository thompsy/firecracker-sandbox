// Package api holds the request/response types shared by firevm and firevmd.
package api

// LaunchRequest contains the data needed to launch a new VM.
type LaunchRequest struct {
	ID  int    `json:"id"`
	Cmd string `json:"cmd"`
}
