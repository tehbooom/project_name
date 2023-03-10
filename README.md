# Project Name Generator

[![Go Report Card](https://goreportcard.com/badge/github.com/tehbooom/project_name)](https://goreportcard.com/report/github.com/tehbooom/project_name)

API where you submit a GET request which responds with a random project name in the form of ADJECTIVE-NOUN

## Running Project Name Generator

Currently this project is only able to run using docker compose. It can however be set up with another postgresql database.

`docker-compose up`

## Getting a new project name

Make a GET request to port 8080 on the box which it is running

### Request

```bash
curl -X GET <IP>:8080/project
```

### Response

```json
[
    {
        "name": "adjective-noun"
    }
]
```

## Nouns

List of [nouns](nouns.text) used

## Adjectives

List of [adjectives](adjectives.text) used
