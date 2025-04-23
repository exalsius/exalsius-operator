# Changelog

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
