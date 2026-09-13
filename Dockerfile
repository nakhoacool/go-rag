FROM golang:alpine AS build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -o /app/rag ./cmd/rag

FROM alpine
RUN apk --no-cache add ca-certificates
WORKDIR /app
COPY --from=build /app/rag .
COPY prompts ./prompts
RUN mkdir -p document/ingest document/processed document/images
EXPOSE 8080
ENV HTTP_ADDR=:8080
CMD ["./rag"]
