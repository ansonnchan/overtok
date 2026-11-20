# syntax=docker/dockerfile:1

FROM golang:1.25.0-alpine3.22 AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY internal ./internal
COPY migrations ./migrations
COPY server ./server
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" \
    -o /out/tokenholic-api ./server/cmd/api

FROM gcr.io/distroless/static-debian13:nonroot

COPY --from=build /out/tokenholic-api /tokenholic-api

EXPOSE 10000
ENTRYPOINT ["/tokenholic-api"]
