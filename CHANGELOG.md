# Changelog

All notable changes to this project will be documented in this file. See [standard-version](https://github.com/conventional-changelog/standard-version) for commit guidelines.

## [1.2.2](https://github.com/djdembeck/bragibooks/compare/v1.2.1...v1.2.2) (2024-08-07)


### Bug Fixes

* :bug: avoid using split for url checking ([c934c61](https://github.com/djdembeck/bragibooks/commit/c934c61dc674bfa62de70d1a140d87caa1f7b489))

### [1.2.1](https://github.com/djdembeck/bragibooks/compare/v1.2.0...v1.2.1) (2023-06-09)


### Bug Fixes

* button disabled bug [#178](https://github.com/djdembeck/bragibooks/issues/178) ([#183](https://github.com/djdembeck/bragibooks/issues/183)) ([ab378d5](https://github.com/djdembeck/bragibooks/commit/ab378d518b37c22535ef4ccae5a2832b093e94b2))

## [1.2.0](https://github.com/djdembeck/bragibooks/compare/v1.0.0...v1.2.0) (2023-05-18)


### Features

* add 'x' to remove search selection ([#166](https://github.com/djdembeck/bragibooks/issues/166)) ([f1f8f6d](https://github.com/djdembeck/bragibooks/commit/f1f8f6ddfcf30a945f263a69ac949c9ed728b460))
* add cover images to search results ([#167](https://github.com/djdembeck/bragibooks/issues/167)) ([e5acbc0](https://github.com/djdembeck/bragibooks/commit/e5acbc0e32b2a3e4d2803994f2352db0763080d0))
* updated the file picker ([#153](https://github.com/djdembeck/bragibooks/issues/153)) ([aed75dd](https://github.com/djdembeck/bragibooks/commit/aed75ddbffc939e0b394fe7fc063fcc92cffeac4))


### Bug Fixes

* :bug: entrypoint wasn't setting user permissions for processes ([b3bd8a7](https://github.com/djdembeck/bragibooks/commit/b3bd8a765c040ce14d57e0b483fdf689669b976b))
* updates to Dockerfile and fix bugs ([#163](https://github.com/djdembeck/bragibooks/issues/163)) ([3e0a7b3](https://github.com/djdembeck/bragibooks/commit/3e0a7b3f13b96307e43ad47222f1b3d4fd1fc726))

## [1.0.0](https://github.com/djdembeck/bragibooks/compare/v0.3.7...v1.0.0) (2023-04-18)


### Features

* Add auto search for books and Add Celery queue and task runner ([#145](https://github.com/djdembeck/bragibooks/issues/145)) ([fb17706](https://github.com/djdembeck/bragibooks/commit/fb17706b9e8e3a50546545ff16d730b7affedcde)), closes [#27](https://github.com/djdembeck/bragibooks/issues/27) [#85](https://github.com/djdembeck/bragibooks/issues/85)


### Bug Fixes

* :bug: search single file now removes extension ([d05a97a](https://github.com/djdembeck/bragibooks/commit/d05a97a322ba8873d63ea55b7e59bc24fe70f229))
* :bug: searches with `&` character could fail ([3ac90c1](https://github.com/djdembeck/bragibooks/commit/3ac90c1c8196db2b63242c9e52af23ca69e6500c))

### [0.3.7](https://github.com/djdembeck/bragibooks/compare/v0.3.6...v0.3.7) (2023-02-24)

### [0.3.6](https://github.com/djdembeck/bragibooks/compare/v0.3.5...v0.3.6) (2022-09-21)


### Features

* allow setting CSRF_TRUSTED_ORIGINS via envvar ([f5c68c4](https://github.com/djdembeck/bragibooks/commit/f5c68c46ab3747f340a048ee96781bda7fa70303))


### Bug Fixes

* :bug: pass original path to `m4b-merge` so it knows what to move to the completed folder ([101e8f2](https://github.com/djdembeck/bragibooks/commit/101e8f25b6c2ecae5715e129e33f425ac485e048))

### [0.3.5](https://github.com/djdembeck/bragibooks/compare/v0.3.4...v0.3.5) (2022-06-12)


### Features

* :sparkles: Allow setting CSRF origins for reverse proxies ([ad91f25](https://github.com/djdembeck/bragibooks/commit/ad91f25050d796ca8ea5bda1e5416f50df4fa1a5))


### Bug Fixes

* :bug: fix cpu count not being set correctly ([748f980](https://github.com/djdembeck/bragibooks/commit/748f98005e8ce0a7852ce84b9ffad48d49f1fba1))
* **docker:** :bug: fix for Docker permissions initialization ([8b8386f](https://github.com/djdembeck/bragibooks/commit/8b8386f7a22c43c6b6532cebbbc8fa65cfc1e277))

### [0.3.4](https://github.com/djdembeck/bragibooks/compare/v0.3.3...v0.3.4) (2022-01-11)


### Features

* :lipstick: add versions to footer ([0969087](https://github.com/djdembeck/bragibooks/commit/0969087b1f96e3dd4e81960e938329e1758dc9b2))

### [0.3.3](https://github.com/djdembeck/bragibooks/compare/v0.3.2...v0.3.3) (2021-12-06)


### Features

* :sparkles: connect output scheme setting to m4b-merge path format ([5521be4](https://github.com/djdembeck/bragibooks/commit/5521be486a260222f01b9ac492f16223b6cdc524))

### [0.3.2](https://github.com/djdembeck/bragibooks/compare/v0.3.1...v0.3.2) (2021-11-18)


### Features

* :sparkles: add settings page ([bf90b57](https://github.com/djdembeck/bragibooks/commit/bf90b57fed20e57ed1f23ef82bad0a378a80cc10))


### Bug Fixes

* :bug: fix order of setting import ([100853a](https://github.com/djdembeck/bragibooks/commit/100853a6202a1d54cc0e8b9538500f26f521566b))
* :bug: show durations longer than 24hrs ([098f376](https://github.com/djdembeck/bragibooks/commit/098f37672ce3f0a1677b016811c0d70c88c26b97))
* **docker:** :ambulance: fix pip package location ([644a422](https://github.com/djdembeck/bragibooks/commit/644a4221512abf844877e56dcdac5b90df3acb3e))
* **model:** :bug: allow runtime to be 0 for podcasts ([8e99513](https://github.com/djdembeck/bragibooks/commit/8e99513643ddaebe27e4c66b73cf4bd673993363))

### [0.4.1](https://github.com/djdembeck/bragibooks/compare/v0.3.1...v0.4.1) (2021-10-18)


### Bug Fixes

* :bug: show durations longer than 24hrs ([098f376](https://github.com/djdembeck/bragibooks/commit/098f37672ce3f0a1677b016811c0d70c88c26b97))
* **model:** :bug: allow runtime to be 0 for podcasts ([8e99513](https://github.com/djdembeck/bragibooks/commit/8e99513643ddaebe27e4c66b73cf4bd673993363))
