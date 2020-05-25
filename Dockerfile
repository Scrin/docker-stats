FROM golang:1.14

COPY . /go/src/docker-stats/
RUN go get docker-stats/...
RUN go install docker-stats

ENTRYPOINT ["docker-stats"]
CMD ["/"]
