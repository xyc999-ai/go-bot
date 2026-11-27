# Third-party access layer

This directory contains code maintained by this project that wraps external SDKs
and APIs. It is not a dependency cache or a copy of vendor repositories.

- aliyun: explicit shared AccessKey clients and ECI request/response wrappers.
- docker: CLI access and controlled Host environment injection.
- llm: compatible protocol encoding, streaming and transport errors.
- deepseek: the dedicated Search API HTTP request/response layer.
- zhipu, doubao, tavily, brave, firecrawl: official search API request/response wrappers;
  Brave uses its LLM Context endpoint. Provider policy remains in providers/web.

Actual libraries are resolved by go.mod/go.sum. Capability decisions, instance
lifecycles, provider selection and Agent execution belong to their respective
providers/application/core layers.
