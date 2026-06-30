FROM golang:1.23-alpine AS build
WORKDIR /src
RUN apk add --no-cache git ca-certificates
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/groupflow-api ./cmd/api
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/groupflow-delivery ./cmd/delivery
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/groupflow-es-indexer ./cmd/es-indexer
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/groupflow-es-backfill ./cmd/es-backfill

FROM alpine:3.20
WORKDIR /app
RUN apk add --no-cache ca-certificates tzdata
COPY --from=build /out/groupflow-api /app/groupflow-api
COPY --from=build /out/groupflow-delivery /app/groupflow-delivery
COPY --from=build /out/groupflow-es-indexer /app/groupflow-es-indexer
COPY --from=build /out/groupflow-es-backfill /app/groupflow-es-backfill
EXPOSE 8080 8082 8083
CMD ["/app/groupflow-api"]
