FROM golang:1.23-alpine AS build
WORKDIR /src
RUN apk add --no-cache git ca-certificates
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/groupflow-api ./cmd/api
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/groupflow-delivery ./cmd/delivery

FROM alpine:3.20
WORKDIR /app
RUN apk add --no-cache ca-certificates tzdata
COPY --from=build /out/groupflow-api /app/groupflow-api
COPY --from=build /out/groupflow-delivery /app/groupflow-delivery
EXPOSE 8080 8082
CMD ["/app/groupflow-api"]
