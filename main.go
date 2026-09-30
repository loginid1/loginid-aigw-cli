package main

import (
	"os"

	"gitlab.com/loginid/software/loginid-aigw-cli.git/cmd"
)

func main() {
	err := cmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}
