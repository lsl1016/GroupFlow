# Elasticsearch + IK 中文分词插件。版本需与 ES 严格一致。
FROM docker.elastic.co/elasticsearch/elasticsearch:8.13.4

RUN curl --fail --location --retry 5 --retry-delay 3 \
        --output /tmp/elasticsearch-analysis-ik.zip \
        https://release.infinilabs.com/analysis-ik/stable/elasticsearch-analysis-ik-8.13.4.zip \
    && bin/elasticsearch-plugin install --batch file:///tmp/elasticsearch-analysis-ik.zip \
    && rm /tmp/elasticsearch-analysis-ik.zip
