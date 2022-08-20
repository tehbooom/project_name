# Project Name Generator

API where you submit a GET request which responds with a random project name in the form of ADJECTIVE-NOUN

## Table of Contents

- [Project Name Generator](#project-name-generator)
  - [Dockerfile](#dockerfile)
  - [Setting environment variables](#setting-environment-variables)
  - [Setup](#setup)
  - [Getting a new project name](#getting-a-new-project-name)
    - [Request](#request)
    - [Response](#response)
  - [Nouns](#nouns)
  - [Adjectives](#adjectives)

## Dockerfile

Build the database with the following

```bash
docker build -t words-db docker/
docker run -d --name words-db -p 5432:5432 words-db
# docker exec -i words-db /bin/bash -c "PGPASSWORD=<password in dockerfile> psql --username postgres words" < words.sql
```

## Setting environment variables

Set the database username, password, and the database name as enviroment variables

```bash
export DB_USERNAME=postgres
export DB_PASSWORD=test
export DB_NAME=words
```

## Setup

First compile the executable 

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

 This is where the[Nouns](https://greenopolis.com/list-of-nouns/) were pulled from

## Adjectives

List of [adjectives](adjectives.text) used

This is where the [Adjectives](https://greenopolis.com/adjectives-list/) were pulled from