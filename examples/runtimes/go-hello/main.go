package main

import (
	"fmt"
	"net/http"
	"os"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "3000"
	}

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintln(w, "Go app jalan di Jabali Panel")
	})

	fmt.Printf("Go example listening on %s\n", port)
	_ = http.ListenAndServe("127.0.0.1:"+port, nil)
}
