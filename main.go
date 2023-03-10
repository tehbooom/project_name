package main

import (
	"os"

	"github.com/tehbooom/project_name/app"
)

func main() {
	a := app.App{}
	a.Initialize(
		os.Getenv("DB_HOST"),
		os.Getenv("DB_PORT"),
		os.Getenv("DB_USERNAME"),
		os.Getenv("DB_PASSWORD"),
		os.Getenv("DB_NAME"))
	a.Run(":8080")
}
