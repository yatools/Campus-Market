FROM golang:1.26-bookworm AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/wutong ./cmd/wutong

FROM debian:bookworm-slim AS runtime

ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl postgresql-client libvips-tools \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY --from=build /out/wutong /app/wutong
RUN useradd --create-home --uid 10001 wutong \
    && chown -R wutong:wutong /app
USER wutong

EXPOSE 8000
ENTRYPOINT ["/app/wutong"]
CMD ["serve", "--addr", "0.0.0.0:8000"]
