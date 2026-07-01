##################################
#         BASE BUILD IMAGE       #
##################################
FROM public.ecr.aws/lts/ubuntu:24.04 AS builder-base

ARG GO_VERSION="1.26.4"
ARG PROJ_VERSION="proj-9.8.1"
ARG PROJECT_FOLDER="/usr/src/gotiler-core"

RUN apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y --fix-missing --no-install-recommends \
    apt-transport-https ca-certificates wget curl zip unzip tar pkg-config \
    git cmake make sqlite3 libsqlite3-dev build-essential \
    && apt-get clean && rm -rf /var/lib/apt/lists/*

WORKDIR /vcpkg
RUN git clone https://github.com/Microsoft/vcpkg.git . \
    && ./bootstrap-vcpkg.sh -disableMetrics

WORKDIR ${PROJECT_FOLDER}
RUN wget -c https://download.osgeo.org/proj/$PROJ_VERSION.tar.gz \
    && tar -xzf $PROJ_VERSION.tar.gz \
    && mkdir $PROJ_VERSION/build

WORKDIR /tmp
RUN wget https://go.dev/dl/go$GO_VERSION.linux-amd64.tar.gz \
    && rm -rf /usr/local/go \
    && tar -C /usr/local -xzf go$GO_VERSION.linux-amd64.tar.gz

ENV PATH="/usr/local/go/bin:${PATH}"


##################################
#       LINUX AMD64 TESTER       #
##################################
FROM builder-base AS linux-amd64-test

RUN /vcpkg/vcpkg install sqlite3[core,tool] tiff --triplet=x64-linux

WORKDIR ${PROJECT_FOLDER}/${PROJ_VERSION}/build
RUN cmake \
    -DCMAKE_TOOLCHAIN_FILE=/vcpkg/scripts/buildsystems/vcpkg.cmake \
    -DVCPKG_TARGET_TRIPLET=x64-linux \
    -DCMAKE_INSTALL_PREFIX=/usr/local/ \
    -DCMAKE_BUILD_TYPE=Release \
    -DBUILD_APPS=OFF \
    -DBUILD_SHARED_LIBS=OFF \
    -DENABLE_CURL=OFF \
    -DENABLE_TIFF=ON \
    -DBUILD_TESTING=OFF \
    -DEMBED_PROJ_DATA_PATH=OFF \
    .. \
    && cmake --build . --config Release -j $(nproc) \
    && cmake --build . --target install -j $(nproc)

WORKDIR ${PROJECT_FOLDER}/build
COPY . .

ENV PKG_CONFIG_PATH="/vcpkg/installed/x64-linux/lib/pkgconfig" \
    CGO_ENABLED="1" \
    CGO_LDFLAGS="-L/vcpkg/installed/x64-linux/lib -g -O2 -static -lstdc++ -lsqlite3 -ltiff -lz -ljpeg -llzma -lm" \
    PROJ_DATA="/usr/local/share/proj"

RUN go test ./...


##################################
#       LINUX ARM64 BUILDER      #
##################################
FROM builder-base AS linux-arm64-builder

RUN apt-get update \
    && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends crossbuild-essential-arm64 \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/*

RUN /vcpkg/vcpkg install sqlite3[core,tool] tiff --triplet=arm64-linux

WORKDIR ${PROJECT_FOLDER}/${PROJ_VERSION}/build
RUN cmake \
    -DCMAKE_TOOLCHAIN_FILE=/vcpkg/scripts/buildsystems/vcpkg.cmake \
    -DVCPKG_TARGET_TRIPLET=arm64-linux \
    -DCMAKE_SYSTEM_NAME=Linux \
    -DCMAKE_SYSTEM_PROCESSOR=aarch64 \
    -DCMAKE_C_COMPILER=aarch64-linux-gnu-gcc \
    -DCMAKE_CXX_COMPILER=aarch64-linux-gnu-g++ \
    -DEXE_SQLITE3=/usr/bin/sqlite3 \
    -DCMAKE_INSTALL_PREFIX=/usr/local/ \
    -DCMAKE_BUILD_TYPE=Release \
    -DBUILD_APPS=OFF \
    -DBUILD_SHARED_LIBS=OFF \
    -DENABLE_CURL=OFF \
    -DENABLE_TIFF=ON \
    -DBUILD_TESTING=OFF \
    -DEMBED_PROJ_DATA_PATH=OFF \
    .. \
    && cmake --build . --config Release -j $(nproc) \
    && cmake --build . --target install -j $(nproc)

WORKDIR ${PROJECT_FOLDER}/build
COPY . .

ENV PKG_CONFIG_PATH="/vcpkg/installed/arm64-linux/lib/pkgconfig" \
    CC="aarch64-linux-gnu-gcc" \
    CXX="aarch64-linux-gnu-g++" \
    GOOS="linux" \
    GOARCH="arm64" \
    CGO_ENABLED="1" \
    CGO_LDFLAGS="-L/vcpkg/installed/arm64-linux/lib -g -O2 -static -lstdc++ -lsqlite3 -ltiff -lz -ljpeg -llzma -lm"

RUN mkdir -p /artifacts/tests/linux-arm64 /artifacts/share/linux-arm64 \
    && for pkg in $(go list ./...); do \
      pkg_name=$(echo "$pkg" | tr '/.' '__'); \
      go test -c -o /artifacts/tests/linux-arm64/${pkg_name}.test $pkg; \
    done \
    && cp -r /usr/local/share/proj/. /artifacts/share/linux-arm64/


##################################
#      WINDOWS AMD64 BUILDER     #
##################################
FROM builder-base AS windows-amd64-builder

RUN apt-get update \
    && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends mingw-w64 pkg-config \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/*

ENV VCPKG_DEFAULT_TRIPLET="x64-mingw-static" \
    VCPKG_DEFAULT_HOST_TRIPLET="x64-mingw-static" \
    CMAKE_C_COMPILER="x86_64-w64-mingw32-gcc" \
    CMAKE_CXX_COMPILER="x86_64-w64-mingw32-g++" \
    CMAKE_SYSTEM_NAME="Windows"

RUN /vcpkg/vcpkg install sqlite3[core,tool] tiff zlib --triplet=x64-mingw-static

WORKDIR ${PROJECT_FOLDER}/${PROJ_VERSION}/build
RUN cmake \
    -DCMAKE_TOOLCHAIN_FILE=/vcpkg/scripts/buildsystems/vcpkg.cmake \
    -DCMAKE_SYSTEM_NAME=Windows \
    -DVCPKG_TARGET_TRIPLET=x64-mingw-static \
    -DCMAKE_C_COMPILER=x86_64-w64-mingw32-gcc \
    -DCMAKE_CXX_COMPILER=x86_64-w64-mingw32-g++ \
    -DEXE_SQLITE3=/usr/bin/sqlite3 \
    -DCMAKE_INSTALL_PREFIX=/usr/local/ \
    -DCMAKE_BUILD_TYPE=Release \
    -DBUILD_APPS=OFF \
    -DBUILD_SHARED_LIBS=OFF \
    -DENABLE_CURL=OFF \
    -DENABLE_TIFF=ON \
    -DBUILD_TESTING=OFF \
    -DEMBED_PROJ_DATA_PATH=OFF \
    .. \
    && cmake --build . --config Release -j $(nproc) \
    && cmake --build . --target install -j $(nproc)

RUN ln -s /usr/local/lib/libproj.a /usr/local/lib/libproj_9.a

WORKDIR ${PROJECT_FOLDER}/build
COPY . .

ENV PKG_CONFIG_PATH="/vcpkg/installed/x64-mingw-static/lib/pkgconfig" \
    CC="x86_64-w64-mingw32-gcc" \
    GOOS="windows" \
    GOARCH="amd64" \
    CGO_ENABLED="1" \
    CGO_LDFLAGS="-L/vcpkg/installed/x64-mingw-static/lib -g -O2 -static -lstdc++ -lsqlite3 -ltiff -lzs -ljpeg -llzma -lm"

RUN mkdir -p /artifacts/tests/windows-amd64 /artifacts/share/windows-amd64 \
    && for pkg in $(go list ./...); do \
      pkg_name=$(echo "$pkg" | tr '/.' '__'); \
      go test -c -o /artifacts/tests/windows-amd64/${pkg_name}.test.exe $pkg; \
    done \
    && cp -r /usr/local/share/proj/. /artifacts/share/windows-amd64/


##################################
#         ARTIFACT TARGETS       #
##################################
FROM scratch AS linux-arm64-artifacts
COPY --from=linux-arm64-builder /artifacts/ /

FROM scratch AS windows-amd64-artifacts
COPY --from=windows-amd64-builder /artifacts/ /
