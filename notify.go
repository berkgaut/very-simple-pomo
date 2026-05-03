package main

import "os/exec"

func execNotify(name string, args ...string) {
	exec.Command(name, args...).Run()
}
