//go:build !darwin && !windows

package main

import "fmt"

func main() {
	fmt.Println("TailSplit menu bar app supports macOS; use the tailsplit CLI on this platform.")
}
