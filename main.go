package main

import (
	"os"

	"gitlab.com/loginid/software/loginid-aigw-cli.git/cmd"
)

func main() {
	if code := cmd.ExitCode(cmd.Execute()); code != 0 {
		os.Exit(code)
	}
}
