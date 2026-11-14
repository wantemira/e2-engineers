FROM golang:1.26.2-bookworm AS source
WORKDIR /workspace
ENV GOTOOLCHAIN=local GOPROXY=off
COPY go.mod ./
COPY internal ./internal
COPY cmd ./cmd

FROM source AS tests
CMD ["go", "test", "./..."]

FROM source AS build
RUN CGO_ENABLED=0 go build -trimpath -o /out/orders ./cmd/api

FROM alpine:3.23 AS runtime
COPY --from=build /out/orders /usr/local/bin/orders
ENV HTTP_ADDR=0.0.0.0:8081
ENTRYPOINT ["orders"]
