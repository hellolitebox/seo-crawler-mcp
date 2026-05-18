FROM silviof/docker-languagetool:latest

USER root
RUN apt-get update \
  && apt-get install -y --no-install-recommends socat \
  && rm -rf /var/lib/apt/lists/*

USER nobody
WORKDIR /LanguageTool-6.6

ENV JAVAOPTIONS="-Xms256m -Xmx768m"

CMD java ${JAVAOPTIONS} -cp languagetool-server.jar org.languagetool.server.HTTPServer --port 8011 --public --allow-origin '*' & \
  exec socat TCP6-LISTEN:8010,fork,reuseaddr,ipv6only=0 TCP:127.0.0.1:8011
