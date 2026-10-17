# syntax=docker/dockerfile:1
FROM golang:1.23-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/styleengine ./cmd/styleengine

FROM debian:bookworm-slim
# Chromium is only used for HTML deck extraction; fonts give it realistic text metrics.
RUN apt-get update \
 && apt-get install -y --no-install-recommends chromium fonts-liberation fonts-dejavu-core ca-certificates \
 && rm -rf /var/lib/apt/lists/* \
 && useradd --system --uid 10001 --home /data app \
 && mkdir -p /data && chown app /data
COPY --from=build /out/styleengine /usr/local/bin/styleengine
ENV CHROME_PATH=/usr/bin/chromium CHROME_NO_SANDBOX=1
USER app
VOLUME /data
EXPOSE 8080
ENTRYPOINT ["styleengine"]
CMD ["serve", "-addr", ":8080", "-data", "/data"]
