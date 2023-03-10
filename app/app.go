package app

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gorilla/mux"
	_ "github.com/lib/pq"
	"github.com/tehbooom/project_name/model"
)

type App struct {
	Router *mux.Router
	DB     *sql.DB
}

func (a *App) Initialize(host, port, user, password, dbname string) {
	dsn := fmt.Sprintf("host=%s port=%s sslmode=disable user=%s password=%s dbname=%s", host, port, user, password, dbname)

	var err error

	a.DB, err = sql.Open("postgres", dsn)
	if err != nil {
		panic(err)
	}

	err = a.DB.Ping()
	if err != nil {
		log.Printf("Error connecting to db: %v\n", err)
	}

	a.initializeDB()

	a.Router = mux.NewRouter()

	a.initializeRoutes()
}

func (a *App) initializeDB() {

	var err error

	words := []string{"noun", "adjective"}

	ctx, cancelfunc := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancelfunc()

	for _, word := range words {

		checkRow := fmt.Sprintf("SELECT COUNT (DISTINCT word) FROM %s WHERE word IS NOT NULL", word)

		table := fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s ( id serial PRIMARY KEY, word text )", word)

		insertRow := fmt.Sprintf("INSERT INTO %s ( word ) VALUES ($1)", word)

		var fileLines []string

		var rowCount int

		file := "words/adjectives.text"

		_, err = a.DB.ExecContext(ctx, table)
		if err != nil {
			panic(err)
		}

		if strings.Compare(word, "noun") == 0 {
			file = "words/nouns.text"
		}

		numRows := a.DB.QueryRow(checkRow)
		err = numRows.Scan(&rowCount)
		if err != nil {
			log.Fatal(err)
		}

		// if table has more than 10000 values break has it already has all words
		if rowCount > 10000 {
			continue
		}

		readFile, err := os.Open(file)

		if err != nil {
			fmt.Println(err)
		}
		fileScanner := bufio.NewScanner(readFile)

		fileScanner.Split(bufio.ScanLines)

		for fileScanner.Scan() {
			fileLines = append(fileLines, fileScanner.Text())
		}

		readFile.Close()

		for _, line := range fileLines {
			_, err = a.DB.ExecContext(ctx, insertRow, line)
			if err != nil {
				panic(err)
			}

		}

		readFile.Close()

		defer a.DB.Close()
	}
}

func (a *App) Run(addr string) {
	log.Fatal(http.ListenAndServe(":8080", a.Router))
}

func (a *App) initializeRoutes() {
	a.Router.HandleFunc("/project", a.getName).Methods("GET")
}

func (a *App) getName(w http.ResponseWriter, r *http.Request) {

	var err error
	n := model.Name{}
	names, errGet := n.Getwords(a.DB)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, err.Error())
		return
	} else if errGet != nil {
		respondWithError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondWithJSON(w, http.StatusOK, names)
}

func respondWithJSON(w http.ResponseWriter, code int, payload interface{}) {
	var err error
	response, _ := json.MarshalIndent(payload, "", "    ")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_, err = w.Write(response)
	if err != nil {
		log.Fatal(err)
	}
}

func respondWithError(w http.ResponseWriter, code int, message string) {
	respondWithJSON(w, code, map[string]string{"error": message})
}
