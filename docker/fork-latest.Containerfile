# CPU server base and the same /app entrypoint/config contract as
# docker/llama-swap.Containerfile, but install the fork's built binary.
FROM ghcr.io/ggml-org/llama.cpp:server@sha256:ae28f5c2046de51dfd00c9116a36fe44b5e8703ef730d0aea69082781eecb1f7

USER root
RUN groupadd --system --gid 10001 app && \
    useradd --system --uid 10001 --gid 10001 --home /app app && \
    mkdir -p /app && chown 10001:10001 /app
ENV HOME=/app
ENV PATH="/app:${PATH}"
USER 10001:10001
WORKDIR /app

COPY --chown=10001:10001 llama-swap /app/llama-swap
COPY --chown=10001:10001 config.yaml /app/config.yaml

EXPOSE 8080
HEALTHCHECK CMD curl -fsS http://127.0.0.1:8080/health || exit 1
ENTRYPOINT ["/app/llama-swap", "-config", "/app/config.yaml", "-watch-config"]
