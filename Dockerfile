FROM golang:1.15 as builder

WORKDIR /opt/

COPY go.mod go.sum ./
RUN go mod download

COPY . ./
RUN go test -cover ./...

ENV GOOS linux
ENV GOARCH amd64
ENV CGO_ENABLED=0

ENV AWS_REGION eu-west-1
ENV CONFIG_FILE /tmp/config.yml

ARG VERSION
RUN go build -v -ldflags "-X main.version=$VERSION" -o yace cmd/yace/main.go

FROM alpine:latest

EXPOSE 5000
ENTRYPOINT ["yace"]
CMD ["--config.file=${CONFIG_FILE}", "--labels-snake-case"]
RUN addgroup -g 1000 exporter && \
    adduser -u 1000 -D -G exporter exporter -h /exporter

WORKDIR /exporter/


RUN apk --no-cache add ca-certificates
COPY --from=builder /opt/yace /usr/local/bin/yace
USER exporter

