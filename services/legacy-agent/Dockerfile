FROM debian:bookworm-slim AS build
ARG LLAMA_CPP_REF=b10797
RUN apt-get update && apt-get install -y --no-install-recommends git cmake build-essential ca-certificates && rm -rf /var/lib/apt/lists/*
RUN git clone --depth 1 --branch ${LLAMA_CPP_REF} https://github.com/ggml-org/llama.cpp.git /src
RUN cmake -S /src -B /src/build -DCMAKE_BUILD_TYPE=Release -DGGML_NATIVE=ON -DGGML_OPENMP=ON -DBUILD_SHARED_LIBS=OFF -DLLAMA_CURL=OFF && cmake --build /src/build -j2 --target llama-server

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends libgomp1 libstdc++6 && rm -rf /var/lib/apt/lists/*
COPY --from=build /src/build/bin/llama-server /usr/local/bin/llama-server
ENTRYPOINT ["llama-server"]
