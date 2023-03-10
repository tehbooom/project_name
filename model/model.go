package model

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"math/rand"
	"strings"
	"time"
)

type Name struct {
	Name string `json:"name"`
}

var projectName string
var err error

func (n *Name) Getwords(DB *sql.DB) ([]Name, error) {
	words := []string{"noun", "adjective"}

	ctx, cancelfunc := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancelfunc()

	var num int
	var adjective string
	var noun string
	min := 2

	for _, word := range words {
		countRow := fmt.Sprintf("SELECT COUNT (DISTINCT word) FROM %s WHERE word IS NOT NULL", word)

		rows := DB.QueryRowContext(ctx, countRow)
		err = rows.Scan(&num)
		if err != nil {
			log.Fatal(err)
		}

		rand.Seed(time.Now().UnixNano())

		randomInt := rand.Intn(num-min+1) + min
		if strings.Compare(word, "noun") == 0 {
			query := fmt.Sprintf("SELECT word FROM %s where id=%d", word, randomInt)
			returnedWord := DB.QueryRowContext(ctx, query)
			err = returnedWord.Scan(&noun)
			if err != nil {
				log.Fatal(err)
			}
		} else {
			query := fmt.Sprintf("SELECT word FROM %s where id=%d", word, randomInt)
			returnedWord := DB.QueryRowContext(ctx, query)
			err = returnedWord.Scan(&adjective)
			if err != nil {
				log.Fatal(err)
			}
		}
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
