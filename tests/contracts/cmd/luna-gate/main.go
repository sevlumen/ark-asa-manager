package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/example/ark-asa-platform/tests/contracts/workflow"
)

func main() {
	handoffPath := flag.String("handoff", "", "release handoff YAML path")
	head := flag.String("head", "", "expected head SHA")
	base := flag.String("base", "", "expected base SHA")
	flag.Parse()
	if *handoffPath == "" || *head == "" || *base == "" || len(flag.Args()) != 0 {
		fmt.Fprintln(os.Stderr, "-handoff, -head, and -base are required")
		flag.Usage()
		os.Exit(2)
	}
	file, err := os.Open(*handoffPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open handoff: %v\n", err)
		os.Exit(1)
	}
	defer file.Close()
	if err := workflow.Validate(file, *head, *base); err != nil {
		fmt.Fprintf(os.Stderr, "invalid release handoff: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("release handoff valid")
}
