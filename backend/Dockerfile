FROM registry-1.docker.io/library/golang:1.26.8-bookworm@sha256:dc9ad6c05acc7a88e5b71bde60a5fe3bd4b9f0db209011711b464107438a8107 AS build
RUN apt-get -o Acquire::Retries=3 -o APT::Update::Error-Mode=any update && \
    apt-get install -y --no-install-recommends poppler-utils && rm -rf /var/lib/apt/lists/*
WORKDIR /src
ARG GOPROXY=https://proxy.golang.org,direct
COPY go.mod go.sum ./
RUN GOPROXY=${GOPROXY} go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/api ./cmd/api && \
    CGO_ENABLED=0 go build -trimpath -o /out/admin ./cmd/admin && \
    CGO_ENABLED=0 go build -trimpath -o /out/collector ./cmd/collector && \
    CGO_ENABLED=0 go build -trimpath -o /out/worker ./cmd/worker

FROM build AS verify
ENV CGO_ENABLED=1 GOTOOLCHAIN=local
CMD ["go", "test", "-race", "-v", "./...", "-count=1", "-timeout=180s"]

FROM registry-1.docker.io/library/debian:bookworm-slim@sha256:7c7b2c966bc9ee8cedfeef67e0e279108992c77681fa595db4a9d65c06ccc587
RUN apt-get -o Acquire::Retries=3 -o APT::Update::Error-Mode=any update && apt-get install -y --no-install-recommends poppler-utils ca-certificates && \
    rm -rf /var/lib/apt/lists/* && mkdir -p /app/private && chown 65532:65532 /app/private && chmod 750 /app/private
COPY --from=build /out/api /app/api
COPY --from=build /out/admin /app/admin
COPY --from=build /out/collector /app/collector
COPY --from=build /out/worker /app/worker
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/app/api"]
