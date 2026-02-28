package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"

	logos "github.com/rphilander/logos/core"
)

func main() {
	sockPath := os.Getenv("LOGOS_SOCK")
	if sockPath == "" {
		sockPath = "/tmp/logos.sock"
	}

	// Read JSON from stdin
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read stdin: %v\n", err)
		os.Exit(1)
	}

	var msg map[string]any
	if err := json.Unmarshal(data, &msg); err != nil {
		fmt.Fprintf(os.Stderr, "parse JSON: %v\n", err)
		os.Exit(1)
	}

	// Add id if missing
	if _, ok := msg["id"]; !ok {
		msg["id"] = logos.NextID()
	}

	// Connect to core
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	// Send request
	if err := logos.WriteMsg(conn, msg); err != nil {
		fmt.Fprintf(os.Stderr, "send: %v\n", err)
		os.Exit(1)
	}

	// Read response
	resp, err := logos.ReadMsg(conn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "receive: %v\n", err)
		os.Exit(1)
	}

	// Print response
	out, err := json.MarshalIndent(resp, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "format response: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(string(out))
}
