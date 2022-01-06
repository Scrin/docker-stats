FROM golang:1.17

WORKDIR /go/src/github.com/Scrin/docker-stats/
COPY . ./
RUN go install .

ENTRYPOINT ["docker-stats"]
CMD ["/"]
