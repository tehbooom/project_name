package model

import (
	"database/sql"
	"io/ioutil"
	"log"
	"math/rand"
	"net/http"
	"regexp"
	"time"

	"github.com/tehbooom/project_name/app"
)

type Name struct {
	Name string `json:"name"`
}

var projectName string

func (n *Name) Getwords(DB *sql.DB) ([]Name, error) {
	var err error
	var aNum int
	aRows := DB.QueryRow("SELECT COUNT (DISTINCT adjective) FROM words WHERE adjective IS NOT NULL")
	err = aRows.Scan(&aNum)
	if err != nil {
		log.Fatal(err)
	}

	var nNum int
	nRows := DB.QueryRow("SELECT COUNT (DISTINCT noun) FROM words WHERE noun IS NOT NULL")
	err = nRows.Scan(&nNum)
	if err != nil {
		log.Fatal(err)
	}

	min := 2
	rand.Seed(time.Now().UnixNano())

	var adjective string
	aRand := rand.Intn(aNum-min+1) + min
	aWord := DB.QueryRow("SELECT adjective FROM words where id=$1", aRand)
	err = aWord.Scan(&adjective)
	if err != nil {
		log.Fatal(err)
	}

	var noun string
	nRand := rand.Intn(nNum-min+1) + min
	nWord := DB.QueryRow("SELECT noun FROM words where id=$1", nRand)
	err = nWord.Scan(&noun)
	if err != nil {
		log.Fatal(err)
	}

	names := []Name{}

	var tempProjectName string = adjective + "-" + noun

	p := &projectName

	if p == &tempProjectName {
		return names, err
	} else {
		var n Name
		projectname := tempProjectName
		n.Name = projectname
		names = append(names, n)
		return names, nil
	}

}

const nurl = "https://greenopolis.com/list-of-nouns/"
const aurl = "https://greenopolis.com/adjectives-list/"

func (a *app.App) initializeDB() {

	// regex
	re := regexp.MustCompile(`<li>(.*?)</li>`)

	// create table
	const table = ` CREATE TABLE [IF NOT EXISTS] words (
		id serial PRIMARY KEY,
		noun text
		adjective text
		
	)`
	a.DB.Exec(table)

	// nouns

	const nrow = `INSERT INTO words (
	noun
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
	const arow = `INSERT INTO words (
		adjective
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
