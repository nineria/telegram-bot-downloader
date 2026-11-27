FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /macdrop .

# Runs as root so it can read the files the Bot API container writes.
FROM scratch
COPY --from=build /macdrop /macdrop
WORKDIR /data
ENTRYPOINT ["/macdrop"]
