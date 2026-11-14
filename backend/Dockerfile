FROM golang:1.26.2-bookworm AS build
WORKDIR /src
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/cmd ./cmd
COPY backend/internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -o /out/ai-buddy-api ./cmd/api
RUN CGO_ENABLED=0 go build -trimpath -o /out/ai-buddy-demo ./cmd/demo

FROM alpine:3.23
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=build /out/ai-buddy-api /usr/local/bin/ai-buddy-api
COPY --from=build /out/ai-buddy-demo /usr/local/bin/ai-buddy-demo
ENTRYPOINT ["ai-buddy-api", "-root", "/app"]
