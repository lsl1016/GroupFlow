# Elasticsearch + IK 中文分词插件。版本需与 ES 严格一致。
FROM docker.elastic.co/elasticsearch/elasticsearch:8.13.4

RUN bin/elasticsearch-plugin install --batch \
    https://github.com/medcl/elasticsearch-analysis-ik/releases/download/v8.13.4/elasticsearch-analysis-ik-8.13.4.zip
