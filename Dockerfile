FROM golang:1.25.7

WORKDIR /go/src/github.com/Scrin/docker-stats/
COPY . ./
RUN go install .

ENTRYPOINT ["docker-stats"]
CMD ["/"]
