package app

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"regexp"

	"github.com/gorilla/mux"
	_ "github.com/lib/pq"
	"github.com/tehbooom/project_name/model"
)

const nurl = "https://greenopolis.com/list-of-nouns/"
const aurl = "https://greenopolis.com/adjectives-list/"

type App struct {
	Router *mux.Router
	DB     *sql.DB
}

func (a *App) Initialize(user, password, dbname string) {
	dsn := fmt.Sprintf("host=localhost port=5432 sslmode=disable user=%s password=%s dbname=%s", user, password, dbname)

	var err error

	a.DB, err = sql.Open("postgres", dsn)
	if err != nil {
		log.Printf("Error connecting to db: %v\n", err)
	}

	a.initializeDB()

	a.Router = mux.NewRouter()

	a.initializeRoutes()
}

func (a *App) initializeDB() {

	// regex
	re := regexp.MustCompile(`<li>(.*?)</li>`)

	// create table
	const nounTable = ` CREATE TABLE [IF NOT EXISTS] nouns (
		id serial PRIMARY KEY,
		word text
	)`

	const adjectiveTable = ` CREATE TABLE [IF NOT EXISTS] adjectives (
		id serial PRIMARY KEY,
		word text
	)`

	a.DB.Exec(nounTable)
	a.DB.Exec(adjectiveTable)

	// nouns

	const nrow = `INSERT INTO nouns (
	word
	)
	VALUES $1
	)`

	nresp, err := http.Get(nurl) // get contents of noun webpage
	if err != nil {
		log.Fatal(err)
	}

	defer nresp.Body.Close()

	nhtml, err := ioutil.ReadAll(nresp.Body)
	if err != nil {
		log.Fatal(err)
	}

	nmatches := re.FindAllStringSubmatch(string(nhtml), -1) // select all words from regex and insert into table
	for _, w := range nmatches {
		a.DB.Exec(nrow, w[1])
	}
	defer a.DB.Close()

	//adjectives
	const arow = `INSERT INTO adjectives (
		word
	)
	VALUES $1
	)`

	aresp, err := http.Get(aurl) // get contents of adjectives webpage
	if err != nil {
		log.Fatal(err)
	}
	defer aresp.Body.Close()
	ahtml, err := ioutil.ReadAll(aresp.Body)
	if err != nil {
		log.Fatal(err)
	}

	amatches := re.FindAllStringSubmatch(string(ahtml), -1) // select all words from regex and insert into table
	for _, w := range amatches {
		a.DB.Exec(arow, w[1])
	}
	defer a.DB.Close()
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
