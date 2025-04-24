package main

import (
	"log"
	"os"

	"github.com/tehbooom/project_name/app"
)

func main() {
	a := app.App{}

	pathName := os.Getenv("PATH_NAME")
	if pathName == "" {
		pathName = "words.json"
	}

	portNumber := os.Getenv("LISTENING_PORT")
	if portNumber == "" {
		portNumber = "8080"
	}

	err := a.Initialize(pathName)
	if err != nil {
		log.Fatal(err)
	}

	a.Run(":" + portNumber)
}
