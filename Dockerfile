#   Copyright 2021 Google LLC
#
#   Licensed under the Apache License, Version 2.0 (the "License");
#   you may not use this file except in compliance with the License.
#   You may obtain a copy of the License at
#
#       http://www.apache.org/licenses/LICENSE-2.0
#
#   Unless required by applicable law or agreed to in writing, software
#   distributed under the License is distributed on an "AS IS" BASIS,
#   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
#   See the License for the specific language governing permissions and
#   limitations under the License.
#
# First stage to build wrapper
FROM golang:1.17-alpine3.15 as build

COPY go.mod $GOPATH/src
COPY go.sum $GOPATH/src
COPY main.go $GOPATH/src
RUN cd $GOPATH/src && go get . && go build -o /main main.go

# Second stage to build final container
FROM google/cloud-sdk:alpine

COPY --from=build /main /bin/long-cloud-run
COPY long-running.sh /long-running.sh
RUN chmod +x /long-running.sh

EXPOSE 8080
ENTRYPOINT ["/bin/long-cloud-run"]
CMD ["/long-running.sh"]