FROM registry.access.redhat.com/ubi8-minimal

LABEL maintainers="kdbdeveloper"
LABEL description="kdb-operator"

RUN microdnf install -y vim iputils

RUN mkdir -p /kdb/bin/ && mkdir -p /kdb/conf/

COPY hack/docker/manager  /kdb/bin/
COPY hack/scripts/*  /kdb/bin/

WORKDIR /home/admin/

RUN  chmod +x /kdb/bin/manager