FROM golang:1.19-alpine

WORKDIR /app/project_name

COPY go.mod .
COPY go.sum .

RUN go mod download

RUN go mod verify

COPY . .

RUN go build -o /project_name

EXPOSE 8080

CMD ["/project_name"]
