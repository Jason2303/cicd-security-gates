package server

import "os/exec"

// RunTool exists only to prove the SAST gate blocks. Do not merge.
func RunTool(path string) error {
cmd := &exec.Cmd{Path: path}
return cmd.Run()
}
