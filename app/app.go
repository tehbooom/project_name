package app

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strconv"

	"github.com/gorilla/mux"
	"github.com/tehbooom/project_name/model"
)

type App struct {
	Router *mux.Router
	Words  *model.WordList
}

type Name struct {
	Name string `json:"name"`
}

type PostRequestBody struct {
	Category string `json:"category"`
	Word     string `json:"word"`
}

func (a *App) Initialize(filename string) error {
	wordList, err := model.LoadWords(filename)
	if err != nil {

	}
	a.Words = wordList

	a.Router = mux.NewRouter()

	a.initializeRoutes()

	return nil
}

func (a *App) Run(addr string) {
	log.Fatal(http.ListenAndServe(":8080", a.Router))
}

func (a *App) initializeRoutes() {
	a.Router.HandleFunc("/project", a.getName).Methods("GET")
	a.Router.HandleFunc("/project", a.addName).Methods("POST")
	a.Router.HandleFunc("/project/{category}/{index}", a.getNameByIndex).Methods("GET")
}

func (a *App) getName(w http.ResponseWriter, r *http.Request) {
	var projectName Name
	projectName.Name = a.Words.Getwords()
	respondWithJSON(w, http.StatusOK, projectName)
}

func (a *App) getNameByIndex(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	index, err := strconv.Atoi(vars["index"])
	if err != nil {
		log.Fatal(err)
	}

	var projectName Name
	projectName.Name = a.Words.GetIndex(vars["category"], index)

	respondWithJSON(w, http.StatusOK, projectName)
}

func (a *App) addName(w http.ResponseWriter, r *http.Request) {
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		respondWithError(w, 404, err.Error())
	}

	defer r.Body.Close()

	var requestBody PostRequestBody

	err = json.Unmarshal(bodyBytes, &requestBody)
	if err != nil {
		log.Fatal(err)
	}

	a.Words.AddWord(requestBody.Category, requestBody.Word)
	index := a.Words.SearchWord(requestBody.Category, requestBody.Word)
	log.Printf("Added word %s to category %s with index %d", requestBody.Category, requestBody.Word, index)
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
