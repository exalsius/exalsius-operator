# Changelog

## 1.0.0 (2025-07-02)


### Features

* add AWS dev environment installation scripts ([#41](https://github.com/exalsius/exalsius-operator/issues/41)) ([d0c1faa](https://github.com/exalsius/exalsius-operator/commit/d0c1faac9f131d3bc62c6dbdb5a927d7abad6999))
* Add RemoteCluster as a cluster type for colonies ([#14](https://github.com/exalsius/exalsius-operator/issues/14)) ([fc59a76](https://github.com/exalsius/exalsius-operator/commit/fc59a7666358b88bba220b92010627237d1900dc))
* add uninstall scripts for the helm/local dev env installation ([#32](https://github.com/exalsius/exalsius-operator/issues/32)) ([26d4bc8](https://github.com/exalsius/exalsius-operator/commit/26d4bc8f04a5933357ff7f4f384158e4dfd1000b))
* **colony:** migrate to k0rdent-based colony implementation and refactor controller logic ([#44](https://github.com/exalsius/exalsius-operator/issues/44)) ([69d45db](https://github.com/exalsius/exalsius-operator/commit/69d45db2a6cf210aa40d360a6e521837e6ff8990))
* Multi-cluster colonies ([#13](https://github.com/exalsius/exalsius-operator/issues/13)) ([1386142](https://github.com/exalsius/exalsius-operator/commit/138614237a0a1d9af53d6209c19b5ed048fb2910))


### Bug Fixes

* add --debug flag to helm install commands ([#35](https://github.com/exalsius/exalsius-operator/issues/35)) ([ef4f5c7](https://github.com/exalsius/exalsius-operator/commit/ef4f5c7d34e4dc9a7a765455c14c1d0e4b8cb506))
* add a further nodePort forward to kind-config.yaml ([f651f3e](https://github.com/exalsius/exalsius-operator/commit/f651f3e24fec60b9c2332a4ac15e4eefa1f44cb4))
* add a getClusterReplicas function to fix nil pointer exception ([665a8a9](https://github.com/exalsius/exalsius-operator/commit/665a8a9801c0a8e10222dcb86a5cbb5c10fa966c))
* add externalAdress to k0smotron control plane ([7b4e9ee](https://github.com/exalsius/exalsius-operator/commit/7b4e9eefbb6a8f5d784c12b3d122a9858f3f18dc))
* add missing examples-values.yaml ([2d94089](https://github.com/exalsius/exalsius-operator/commit/2d940890fef2ad694fa9f93cd0d61002e0d82ae3))
* add missing helm dependency update and helm repo add cmd to install-via-helm.sh ([36c87c1](https://github.com/exalsius/exalsius-operator/commit/36c87c1ebc88ceb35306fb221867c35f296b319b))
* add path to .release-please-manifest.json ([#23](https://github.com/exalsius/exalsius-operator/issues/23)) ([e701f2c](https://github.com/exalsius/exalsius-operator/commit/e701f2c1714d0d781cbe5b620897c925dafeb93e))
* add release please component name ([#60](https://github.com/exalsius/exalsius-operator/issues/60)) ([ee5c333](https://github.com/exalsius/exalsius-operator/commit/ee5c33350b29ff7d5db300d34c56db1a6a88ff85))
* add release-please config file to also bump helm charts ([#53](https://github.com/exalsius/exalsius-operator/issues/53)) ([e7da1c6](https://github.com/exalsius/exalsius-operator/commit/e7da1c61105532b9edc2937e22ac4f372ec373b3))
* add SCRIPT_DIR to install-via-helm.sh and use absolute path ([d57066c](https://github.com/exalsius/exalsius-operator/commit/d57066c8ec4be38834d790055c438b607f8fa346))
* adjust automatic helm chart packaging and release management ([#57](https://github.com/exalsius/exalsius-operator/issues/57)) ([16a8574](https://github.com/exalsius/exalsius-operator/commit/16a85745e029512bcfd4c9028bfbb6b7a82fa948))
* adjust AWSClusterSpec for hosted control planes ([08ff4bf](https://github.com/exalsius/exalsius-operator/commit/08ff4bf20058ede163ada706556128bdaefd810a))
* fix relative paths ([356ff51](https://github.com/exalsius/exalsius-operator/commit/356ff51bc208645a3bff1ae503229380692ef5bd))
* fix release please management ([#58](https://github.com/exalsius/exalsius-operator/issues/58)) ([052a9da](https://github.com/exalsius/exalsius-operator/commit/052a9da66e1f3c016a7430c4a6d24bd1a6b446b6))
* fix typo in github workflow ([ee43d24](https://github.com/exalsius/exalsius-operator/commit/ee43d24e8a1702db9003e8894e174f0a86aa7ea2))
* flaky cluster-api-provider-aws deployment ([c345688](https://github.com/exalsius/exalsius-operator/commit/c34568886ed16e5a7c6c9b152e830912ec4ca456))
* github workflow secrets must not start with GITHUB ([dd5aced](https://github.com/exalsius/exalsius-operator/commit/dd5aced1f6af5a5079d05d56e928d4a030cbf6c2))
* helm chart release management ([#55](https://github.com/exalsius/exalsius-operator/issues/55)) ([12fc36c](https://github.com/exalsius/exalsius-operator/commit/12fc36c05e0a4c75e0a0babe04cbf301fcb1fa67))
* **Helm:** Add missing new DDPJob CRD and Helm AddonProvider ([#6](https://github.com/exalsius/exalsius-operator/issues/6)) ([64c1cff](https://github.com/exalsius/exalsius-operator/commit/64c1cffa2cbc00471317a5ecbc128257e6e2baab))
* increase memory limit of k0smotron control plane manager pod ([894be83](https://github.com/exalsius/exalsius-operator/commit/894be8335b5060b3ef4b74bc9988d6ba470fd614))
* local docker colony clusters need a fixed nodePort that maps to kind-config ([0ea9ff4](https://github.com/exalsius/exalsius-operator/commit/0ea9ff422facc97f57a3002cb2c33811523e562f))
* only use --debug in helm operator install ([#38](https://github.com/exalsius/exalsius-operator/issues/38)) ([ad69bcb](https://github.com/exalsius/exalsius-operator/commit/ad69bcb0ae7ac29f25768615b90312d040e59693))
* release please version management ([#62](https://github.com/exalsius/exalsius-operator/issues/62)) ([877f93a](https://github.com/exalsius/exalsius-operator/commit/877f93a14e96c74005eda86108161ca4e60ad2f8))
* **release:** set manifest-file path in release please github action ([#64](https://github.com/exalsius/exalsius-operator/issues/64)) ([85c9bcb](https://github.com/exalsius/exalsius-operator/commit/85c9bcb3090898856736d4c102cdabe2b1613b6c))
* remove merge conflict residues ([#52](https://github.com/exalsius/exalsius-operator/issues/52)) ([9510f55](https://github.com/exalsius/exalsius-operator/commit/9510f552731ad6a711182003faa8c08406aec068))
* remove unused .release-please-manifest.json ([#25](https://github.com/exalsius/exalsius-operator/issues/25)) ([68c414b](https://github.com/exalsius/exalsius-operator/commit/68c414b0a56203bc9e0b4cf2faef8819e835a6a2))
* set correct permission for post-release docker push job ([#29](https://github.com/exalsius/exalsius-operator/issues/29)) ([e128425](https://github.com/exalsius/exalsius-operator/commit/e12842599c3db4e8deafc8e1b132124ff2d82f88))

## [0.3.1](https://github.com/exalsius/exalsius-operator/compare/v0.3.0...v0.3.1) (2025-07-02)


### Bug Fixes

* add release-please config file to also bump helm charts ([#53](https://github.com/exalsius/exalsius-operator/issues/53)) ([e7da1c6](https://github.com/exalsius/exalsius-operator/commit/e7da1c61105532b9edc2937e22ac4f372ec373b3))
* helm chart release management ([#55](https://github.com/exalsius/exalsius-operator/issues/55)) ([12fc36c](https://github.com/exalsius/exalsius-operator/commit/12fc36c05e0a4c75e0a0babe04cbf301fcb1fa67))

## [0.3.0](https://github.com/exalsius/exalsius-operator/compare/v0.2.0...v0.3.0) (2025-06-30)


### Features

* **colony:** migrate to k0rdent-based colony implementation and refactor controller logic ([#44](https://github.com/exalsius/exalsius-operator/issues/44)) ([69d45db](https://github.com/exalsius/exalsius-operator/commit/69d45db2a6cf210aa40d360a6e521837e6ff8990))


### Bug Fixes

* remove merge conflict residues ([#52](https://github.com/exalsius/exalsius-operator/issues/52)) ([9510f55](https://github.com/exalsius/exalsius-operator/commit/9510f552731ad6a711182003faa8c08406aec068))

## [0.2.0](https://github.com/exalsius/exalsius-operator/compare/v0.1.2...v0.2.0) (2025-04-23)


### Features

* add AWS dev environment installation scripts ([#41](https://github.com/exalsius/exalsius-operator/issues/41)) ([d0c1faa](https://github.com/exalsius/exalsius-operator/commit/d0c1faac9f131d3bc62c6dbdb5a927d7abad6999))
* add uninstall scripts for the helm/local dev env installation ([#32](https://github.com/exalsius/exalsius-operator/issues/32)) ([26d4bc8](https://github.com/exalsius/exalsius-operator/commit/26d4bc8f04a5933357ff7f4f384158e4dfd1000b))


### Bug Fixes

* add --debug flag to helm install commands ([#35](https://github.com/exalsius/exalsius-operator/issues/35)) ([ef4f5c7](https://github.com/exalsius/exalsius-operator/commit/ef4f5c7d34e4dc9a7a765455c14c1d0e4b8cb506))
* add a getClusterReplicas function to fix nil pointer exception ([665a8a9](https://github.com/exalsius/exalsius-operator/commit/665a8a9801c0a8e10222dcb86a5cbb5c10fa966c))
* adjust AWSClusterSpec for hosted control planes ([08ff4bf](https://github.com/exalsius/exalsius-operator/commit/08ff4bf20058ede163ada706556128bdaefd810a))
* increase memory limit of k0smotron control plane manager pod ([894be83](https://github.com/exalsius/exalsius-operator/commit/894be8335b5060b3ef4b74bc9988d6ba470fd614))
* only use --debug in helm operator install ([#38](https://github.com/exalsius/exalsius-operator/issues/38)) ([ad69bcb](https://github.com/exalsius/exalsius-operator/commit/ad69bcb0ae7ac29f25768615b90312d040e59693))

## [0.1.2](https://github.com/exalsius/exalsius-operator/compare/v0.1.1...v0.1.2) (2025-03-28)


### Bug Fixes

* set correct permission for post-release docker push job ([#29](https://github.com/exalsius/exalsius-operator/issues/29)) ([e128425](https://github.com/exalsius/exalsius-operator/commit/e12842599c3db4e8deafc8e1b132124ff2d82f88))

## [0.1.1](https://github.com/exalsius/exalsius-operator/compare/v0.1.0...v0.1.1) (2025-03-26)


### Bug Fixes

* remove unused .release-please-manifest.json ([#25](https://github.com/exalsius/exalsius-operator/issues/25)) ([68c414b](https://github.com/exalsius/exalsius-operator/commit/68c414b0a56203bc9e0b4cf2faef8819e835a6a2))
