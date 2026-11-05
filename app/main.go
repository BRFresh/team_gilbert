package main

import (
	"context"
	"fmt"
	"os"
)

func main() {
	statuses, err := GetNodeStatuses(context.Background())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	for _, status := range statuses {
		fmt.Println(status)
	}
}
