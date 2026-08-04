# Build stage
FROM golang:1.24.2-alpine3.21 AS builder
WORKDIR /app
COPY . .
RUN go build -o main main.go

# Run stage
FROM alpine:3.21
WORKDIR /app
COPY --from=builder /app/main .
# No app.env here on purpose: baking config into an image layer means the secrets
# ship with the image and sit in the registry for anyone with pull access. The
# config comes from the environment instead -- a Kubernetes Secret in production,
# app.env locally -- which util.LoadConfig falls back to when no file is present.
COPY start.sh .
COPY wait-for.sh .
COPY db/migration ./db/migration

EXPOSE 8080
CMD [ "/app/main" ]
ENTRYPOINT [ "/app/start.sh" ]
