package model

import (
	"database/sql"
	"log"
	"math/rand"
	"time"
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
