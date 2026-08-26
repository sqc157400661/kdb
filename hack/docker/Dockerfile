FROM kdbdeveloper/ubi8-base

LABEL maintainers="kdbdeveloper"
LABEL description="kdb-operator"


RUN mkdir -p /kdb/bin/ && mkdir -p /kdb/conf/

COPY hack/docker/manager  /kdb/bin/
COPY hack/docker/alert-relay  /kdb/bin/
COPY hack/scripts/*  /kdb/bin/

WORKDIR /home/admin/

RUN  chmod +x /kdb/bin/*
