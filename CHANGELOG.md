# Changelog

## [0.4.6](https://github.com/exalsius/exalsius-operator/compare/v0.4.5...v0.4.6) (2025-11-29)


### Bug Fixes

* **netbird:** delete netbird agent when vpn cluster is deleted ([#111](https://github.com/exalsius/exalsius-operator/issues/111)) ([1c68f26](https://github.com/exalsius/exalsius-operator/commit/1c68f2632d5a8137f9fe32c183286d1066cc6abd))

## [0.4.5](https://github.com/exalsius/exalsius-operator/compare/v0.4.4...v0.4.5) (2025-11-27)


### Bug Fixes

* **netbird:** reorder netbird resource deletion ([#109](https://github.com/exalsius/exalsius-operator/issues/109)) ([3b936ff](https://github.com/exalsius/exalsius-operator/commit/3b936ffc31e630efab4674ad968a2329b2f75739))

## [0.4.4](https://github.com/exalsius/exalsius-operator/compare/v0.4.3...v0.4.4) (2025-11-27)


### Bug Fixes

* **netbird:** check if netbird already exists while bootrapping ([#107](https://github.com/exalsius/exalsius-operator/issues/107)) ([5a90134](https://github.com/exalsius/exalsius-operator/commit/5a90134ab2d1730a0f7131fbd4bbe91b6c11abfc))

## [0.4.3](https://github.com/exalsius/exalsius-operator/compare/v0.4.2...v0.4.3) (2025-11-27)


### Bug Fixes

* **netbird:** refactor netbird client to directly use the API and fix groups ([#105](https://github.com/exalsius/exalsius-operator/issues/105)) ([d4f8d1e](https://github.com/exalsius/exalsius-operator/commit/d4f8d1e1a141176525e41bf03eb3894fad45fe2e))

## [0.4.2](https://github.com/exalsius/exalsius-operator/compare/v0.4.1...v0.4.2) (2025-11-26)


### Bug Fixes

* **netbird:** use server-side apply to fix conflicts with capi ([#103](https://github.com/exalsius/exalsius-operator/issues/103)) ([149615e](https://github.com/exalsius/exalsius-operator/commit/149615e3522d71edd337799dc900d965ad5740da))

## [0.4.1](https://github.com/exalsius/exalsius-operator/compare/v0.4.0...v0.4.1) (2025-11-26)


### Bug Fixes

* **netbird:** set correct cp api endpoints for cilium ([6161a90](https://github.com/exalsius/exalsius-operator/commit/6161a90bd2479f326a79b47b2a4e779fa9afc900))

## [0.4.0](https://github.com/exalsius/exalsius-operator/compare/v0.3.5...v0.4.0) (2025-11-26)


### Features

* **api:** add NetBird integration types to Colony CRD ([efcf323](https://github.com/exalsius/exalsius-operator/commit/efcf323f1f71eba70f53c7a38d4cbb03ebe018a3))
* **controller:** integrate NetBird into Colony reconciliation ([872c54f](https://github.com/exalsius/exalsius-operator/commit/872c54feaf533ff295ca6b5fee0a624a93fcb98e))
* **netbird:** add routing peer deployment for NodePort clusters ([576e2c4](https://github.com/exalsius/exalsius-operator/commit/576e2c49bccda68860e2954a1f62f1dee6ce1f12))
* **netbird:** implement API client and resource management ([33ca0a0](https://github.com/exalsius/exalsius-operator/commit/33ca0a00106d215c2816ea6208b8630e1a979f87))
* **netbird:** implement bootstrap secret patching ([e36ea39](https://github.com/exalsius/exalsius-operator/commit/e36ea39ac4ec055665ffaec9745d9e70e37e901b))


### Bug Fixes

* **lint:** resolve errcheck, goconst, unparam, and gofmt issues ([115a4d7](https://github.com/exalsius/exalsius-operator/commit/115a4d7fef39898c1c7c54ad4f32eaabf24b4a8d))
* **netbird:** correct resource cleanup order ([853afef](https://github.com/exalsius/exalsius-operator/commit/853afefce10603933563e6aa9f6956da71eccfb0))

## [0.3.5](https://github.com/exalsius/exalsius-operator/compare/v0.3.4...v0.3.5) (2025-10-19)


### Bug Fixes

* preserve external labels during ClusterDeployment updates ([#94](https://github.com/exalsius/exalsius-operator/issues/94)) ([ac9277f](https://github.com/exalsius/exalsius-operator/commit/ac9277faf6c8c4ed9973803ba2244b5f3ea830a8))
* remove unnessary release please config ([#95](https://github.com/exalsius/exalsius-operator/issues/95)) ([217a38b](https://github.com/exalsius/exalsius-operator/commit/217a38b717d3909f35ec9d17084804bf50f8be44))

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
