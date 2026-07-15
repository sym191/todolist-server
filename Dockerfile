FROM golang:1.26.5-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/todolist-api ./cmd/api

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/todolist-api /todolist-api
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/todolist-api"]
