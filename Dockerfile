FROM golang:1.26-alpine AS build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .

ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_TIME=unknown
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath -ldflags="-s -w \
      -X main.version=${VERSION} \
      -X main.commit=${COMMIT} \
      -X main.buildTime=${BUILD_TIME}" \
    -o craaft-mcp .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /app/craaft-mcp /craaft-mcp
EXPOSE 8080
ENTRYPOINT ["/craaft-mcp"]
