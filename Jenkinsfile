pipeline {
    agent none

    triggers {
        // Devel build at 3am UTC (matches GitHub Actions devel.yml) and
        // devel devel image rebuild at 4am UTC (matches docker.yml build-devel job)
        cron('0 3 * * *\n0 4 * * *')
    }

    environment {
        PROJECT_NAME = 'shortner'
        PROJECT_ORG = 'apimgr'
        BINDIR = 'binaries'
        RELDIR = 'releases'
        INTERNAL_NAME = 'shortner'
        // Jenkins-native Go caches: persistent directories on the agent under JENKINS_HOME, never user home paths
        GO_CACHE = "${JENKINS_HOME}/cache/${PROJECT_ORG}/${PROJECT_NAME}/go-mod"
        GO_BUILD = "${JENKINS_HOME}/cache/${PROJECT_ORG}/${PROJECT_NAME}/go-build"

        // =========================================================================
        // GIT PROVIDER CONFIGURATION
        // Uncomment ONE block below based on your git hosting platform
        // =========================================================================

        // ----- GITHUB (default) -----
        GIT_FQDN = 'github.com'
        // Jenkins credentials ID; bind only inside trusted registry publishing steps
        GIT_TOKEN_CREDENTIALS = 'github-token'
        REGISTRY = "ghcr.io/${PROJECT_ORG}/${INTERNAL_NAME}"

        // ----- GITEA / FORGEJO (self-hosted) -----
        // GIT_FQDN = 'git.example.com'  // Your Gitea/Forgejo domain
        // GIT_TOKEN_CREDENTIALS = 'gitea-token'  // Jenkins credentials ID
        // REGISTRY = "${GIT_FQDN}/${PROJECT_ORG}/${INTERNAL_NAME}"

        // ----- GITLAB (gitlab.com or self-hosted) -----
        // GIT_FQDN = 'gitlab.com'  // or your self-hosted GitLab domain
        // GIT_TOKEN_CREDENTIALS = 'gitlab-token'  // Jenkins credentials ID
        // REGISTRY = "registry.${GIT_FQDN}/${PROJECT_ORG}/${INTERNAL_NAME}"

        // ----- DOCKER HUB -----
        // GIT_FQDN = 'github.com'  // Git provider (separate from registry)
        // GIT_TOKEN_CREDENTIALS = 'github-token'
        // REGISTRY = "docker.io/${PROJECT_ORG}/${INTERNAL_NAME}"

        // =========================================================================
    }

    stages {
        stage('Setup') {
            agent { label 'amd64' }
            steps {
                script {
                    // Determine build type and version
                    if (env.TAG_NAME?.endsWith('-beta')) {
                        // Beta build (tag YYYYMMDDHHMMSS-beta) - matches beta.yml
                        env.BUILD_TYPE = 'beta'
                        env.VERSION = env.TAG_NAME
                    } else if (env.TAG_NAME) {
                        // Release build (tag push) - matches release.yml
                        env.BUILD_TYPE = 'release'
                        env.VERSION = env.TAG_NAME.replaceFirst('^v', '')
                    } else if (env.BRANCH_NAME == 'main') {
                        // Devel build - matches devel.yml
                        // Devel identity is the commit being built, not a timestamp
                        env.BUILD_TYPE = 'devel'
                        env.VERSION = sh(script: 'git rev-parse --short=7 HEAD', returnStdout: true).trim()
                    } else {
                        // Other branches - dev build
                        env.BUILD_TYPE = 'dev'
                        env.VERSION = sh(script: 'date -u +"%Y%m%d%H%M%S"', returnStdout: true).trim() + '-dev'
                    }
                    env.COMMIT_ID = sh(script: 'git rev-parse --short=7 HEAD', returnStdout: true).trim()
                    env.BUILD_EPOCH = sh(script: 'date -u +%s', returnStdout: true).trim()
                    env.BUILD_DATE = sh(script: "date -u -d @${env.BUILD_EPOCH} +\"%Y-%m-%dT%H:%M:%SZ\"", returnStdout: true).trim()
                    // OFFICIAL_SITE (optional): site.txt wins; otherwise use Jenkins credentials or leave empty
                    // Never guess or assume - must be explicitly defined by user
                    env.OFFICIAL_SITE = sh(script: '[ -f site.txt ] && cat site.txt || echo "${OFFICIAL_SITE:-}"', returnStdout: true).trim()
                    env.LDFLAGS = "-s -w -X 'github.com/apimgr/shortner/src/common/version.Version=${env.VERSION}' -X 'github.com/apimgr/shortner/src/common/version.CommitID=${env.COMMIT_ID}' -X 'github.com/apimgr/shortner/src/common/version.BuildEpoch=${env.BUILD_EPOCH}' -X 'github.com/apimgr/shortner/src/common/version.OfficialSite=${env.OFFICIAL_SITE}'"
                    env.HAS_CLI = sh(script: '[ -d src/client ] && echo true || echo false', returnStdout: true).trim()
                }
                sh 'mkdir -p ${BINDIR} ${RELDIR} ${GO_CACHE} ${GO_BUILD}'
                echo "Build type: ${BUILD_TYPE}, Version: ${VERSION}"
            }
        }

        stage('Build Server') {
            parallel {
                // Linux
                stage('Linux AMD64') {
                    agent { label 'amd64' }
                    steps {
                        sh '''
                            timeout -k 30 3600 docker run --rm --no-healthcheck \
                                --name "${PROJECT_NAME}-$(head -c6 /dev/urandom | od -An -tx1 | tr -d ' \n' | cut -c1-8)" \
                                -v ${WORKSPACE}:/app \
                                -v ${GO_CACHE}:/usr/local/share/go/pkg/mod \
                                -v ${GO_BUILD}:/usr/local/share/go/cache \
                                -w /app \
                                -e CGO_ENABLED=0 \
                                -e GOOS=linux \
                                -e GOARCH=amd64 \
                                casjaysdev/go:latest \
                                go build -buildvcs=false -trimpath -ldflags "${LDFLAGS}" -o ${BINDIR}/${PROJECT_NAME}-linux-amd64 ./src
                        '''
                    }
                }
                stage('Linux ARM64') {
                    agent { label 'arm64' }
                    steps {
                        sh '''
                            timeout -k 30 3600 docker run --rm --no-healthcheck \
                                --name "${PROJECT_NAME}-$(head -c6 /dev/urandom | od -An -tx1 | tr -d ' \n' | cut -c1-8)" \
                                -v ${WORKSPACE}:/app \
                                -v ${GO_CACHE}:/usr/local/share/go/pkg/mod \
                                -v ${GO_BUILD}:/usr/local/share/go/cache \
                                -w /app \
                                -e CGO_ENABLED=0 \
                                -e GOOS=linux \
                                -e GOARCH=arm64 \
                                casjaysdev/go:latest \
                                go build -buildvcs=false -trimpath -ldflags "${LDFLAGS}" -o ${BINDIR}/${PROJECT_NAME}-linux-arm64 ./src
                        '''
                    }
                }
                // Darwin (macOS)
                stage('Darwin AMD64') {
                    agent { label 'amd64' }
                    steps {
                        sh '''
                            timeout -k 30 3600 docker run --rm --no-healthcheck \
                                --name "${PROJECT_NAME}-$(head -c6 /dev/urandom | od -An -tx1 | tr -d ' \n' | cut -c1-8)" \
                                -v ${WORKSPACE}:/app \
                                -v ${GO_CACHE}:/usr/local/share/go/pkg/mod \
                                -v ${GO_BUILD}:/usr/local/share/go/cache \
                                -w /app \
                                -e CGO_ENABLED=0 \
                                -e GOOS=darwin \
                                -e GOARCH=amd64 \
                                casjaysdev/go:latest \
                                go build -buildvcs=false -trimpath -ldflags "${LDFLAGS}" -o ${BINDIR}/${PROJECT_NAME}-darwin-amd64 ./src
                        '''
                    }
                }
                stage('Darwin ARM64') {
                    agent { label 'amd64' }
                    steps {
                        sh '''
                            timeout -k 30 3600 docker run --rm --no-healthcheck \
                                --name "${PROJECT_NAME}-$(head -c6 /dev/urandom | od -An -tx1 | tr -d ' \n' | cut -c1-8)" \
                                -v ${WORKSPACE}:/app \
                                -v ${GO_CACHE}:/usr/local/share/go/pkg/mod \
                                -v ${GO_BUILD}:/usr/local/share/go/cache \
                                -w /app \
                                -e CGO_ENABLED=0 \
                                -e GOOS=darwin \
                                -e GOARCH=arm64 \
                                casjaysdev/go:latest \
                                go build -buildvcs=false -trimpath -ldflags "${LDFLAGS}" -o ${BINDIR}/${PROJECT_NAME}-darwin-arm64 ./src
                        '''
                    }
                }
                // Windows
                stage('Windows AMD64') {
                    agent { label 'amd64' }
                    steps {
                        sh '''
                            timeout -k 30 3600 docker run --rm --no-healthcheck \
                                --name "${PROJECT_NAME}-$(head -c6 /dev/urandom | od -An -tx1 | tr -d ' \n' | cut -c1-8)" \
                                -v ${WORKSPACE}:/app \
                                -v ${GO_CACHE}:/usr/local/share/go/pkg/mod \
                                -v ${GO_BUILD}:/usr/local/share/go/cache \
                                -w /app \
                                -e CGO_ENABLED=0 \
                                -e GOOS=windows \
                                -e GOARCH=amd64 \
                                casjaysdev/go:latest \
                                go build -buildvcs=false -trimpath -ldflags "${LDFLAGS}" -o ${BINDIR}/${PROJECT_NAME}-windows-amd64.exe ./src
                        '''
                    }
                }
                stage('Windows ARM64') {
                    agent { label 'amd64' }
                    steps {
                        sh '''
                            timeout -k 30 3600 docker run --rm --no-healthcheck \
                                --name "${PROJECT_NAME}-$(head -c6 /dev/urandom | od -An -tx1 | tr -d ' \n' | cut -c1-8)" \
                                -v ${WORKSPACE}:/app \
                                -v ${GO_CACHE}:/usr/local/share/go/pkg/mod \
                                -v ${GO_BUILD}:/usr/local/share/go/cache \
                                -w /app \
                                -e CGO_ENABLED=0 \
                                -e GOOS=windows \
                                -e GOARCH=arm64 \
                                casjaysdev/go:latest \
                                go build -buildvcs=false -trimpath -ldflags "${LDFLAGS}" -o ${BINDIR}/${PROJECT_NAME}-windows-arm64.exe ./src
                        '''
                    }
                }
                // FreeBSD
                stage('FreeBSD AMD64') {
                    agent { label 'amd64' }
                    steps {
                        sh '''
                            timeout -k 30 3600 docker run --rm --no-healthcheck \
                                --name "${PROJECT_NAME}-$(head -c6 /dev/urandom | od -An -tx1 | tr -d ' \n' | cut -c1-8)" \
                                -v ${WORKSPACE}:/app \
                                -v ${GO_CACHE}:/usr/local/share/go/pkg/mod \
                                -v ${GO_BUILD}:/usr/local/share/go/cache \
                                -w /app \
                                -e CGO_ENABLED=0 \
                                -e GOOS=freebsd \
                                -e GOARCH=amd64 \
                                casjaysdev/go:latest \
                                go build -buildvcs=false -trimpath -ldflags "${LDFLAGS}" -o ${BINDIR}/${PROJECT_NAME}-freebsd-amd64 ./src
                        '''
                    }
                }
                stage('FreeBSD ARM64') {
                    agent { label 'amd64' }
                    steps {
                        sh '''
                            timeout -k 30 3600 docker run --rm --no-healthcheck \
                                --name "${PROJECT_NAME}-$(head -c6 /dev/urandom | od -An -tx1 | tr -d ' \n' | cut -c1-8)" \
                                -v ${WORKSPACE}:/app \
                                -v ${GO_CACHE}:/usr/local/share/go/pkg/mod \
                                -v ${GO_BUILD}:/usr/local/share/go/cache \
                                -w /app \
                                -e CGO_ENABLED=0 \
                                -e GOOS=freebsd \
                                -e GOARCH=arm64 \
                                casjaysdev/go:latest \
                                go build -buildvcs=false -trimpath -ldflags "${LDFLAGS}" -o ${BINDIR}/${PROJECT_NAME}-freebsd-arm64 ./src
                        '''
                    }
                }
            }
        }

        // CLI builds - only if src/client/ exists (matches GitHub Actions)
        stage('Build CLI') {
            when {
                expression { env.HAS_CLI == 'true' }
            }
            parallel {
                stage('CLI Linux AMD64') {
                    agent { label 'amd64' }
                    steps {
                        sh '''
                            timeout -k 30 3600 docker run --rm --no-healthcheck \
                                --name "${PROJECT_NAME}-$(head -c6 /dev/urandom | od -An -tx1 | tr -d ' \n' | cut -c1-8)" \
                                -v ${WORKSPACE}:/app \
                                -v ${GO_CACHE}:/usr/local/share/go/pkg/mod \
                                -v ${GO_BUILD}:/usr/local/share/go/cache \
                                -w /app \
                                -e CGO_ENABLED=0 \
                                -e GOOS=linux \
                                -e GOARCH=amd64 \
                                casjaysdev/go:latest \
                                go build -buildvcs=false -trimpath -ldflags "${LDFLAGS}" -o ${BINDIR}/${PROJECT_NAME}-cli-linux-amd64 ./src/client
                        '''
                    }
                }
                stage('CLI Linux ARM64') {
                    agent { label 'arm64' }
                    steps {
                        sh '''
                            timeout -k 30 3600 docker run --rm --no-healthcheck \
                                --name "${PROJECT_NAME}-$(head -c6 /dev/urandom | od -An -tx1 | tr -d ' \n' | cut -c1-8)" \
                                -v ${WORKSPACE}:/app \
                                -v ${GO_CACHE}:/usr/local/share/go/pkg/mod \
                                -v ${GO_BUILD}:/usr/local/share/go/cache \
                                -w /app \
                                -e CGO_ENABLED=0 \
                                -e GOOS=linux \
                                -e GOARCH=arm64 \
                                casjaysdev/go:latest \
                                go build -buildvcs=false -trimpath -ldflags "${LDFLAGS}" -o ${BINDIR}/${PROJECT_NAME}-cli-linux-arm64 ./src/client
                        '''
                    }
                }
                stage('CLI Darwin AMD64') {
                    agent { label 'amd64' }
                    steps {
                        sh '''
                            timeout -k 30 3600 docker run --rm --no-healthcheck \
                                --name "${PROJECT_NAME}-$(head -c6 /dev/urandom | od -An -tx1 | tr -d ' \n' | cut -c1-8)" \
                                -v ${WORKSPACE}:/app \
                                -v ${GO_CACHE}:/usr/local/share/go/pkg/mod \
                                -v ${GO_BUILD}:/usr/local/share/go/cache \
                                -w /app \
                                -e CGO_ENABLED=0 \
                                -e GOOS=darwin \
                                -e GOARCH=amd64 \
                                casjaysdev/go:latest \
                                go build -buildvcs=false -trimpath -ldflags "${LDFLAGS}" -o ${BINDIR}/${PROJECT_NAME}-cli-darwin-amd64 ./src/client
                        '''
                    }
                }
                stage('CLI Darwin ARM64') {
                    agent { label 'amd64' }
                    steps {
                        sh '''
                            timeout -k 30 3600 docker run --rm --no-healthcheck \
                                --name "${PROJECT_NAME}-$(head -c6 /dev/urandom | od -An -tx1 | tr -d ' \n' | cut -c1-8)" \
                                -v ${WORKSPACE}:/app \
                                -v ${GO_CACHE}:/usr/local/share/go/pkg/mod \
                                -v ${GO_BUILD}:/usr/local/share/go/cache \
                                -w /app \
                                -e CGO_ENABLED=0 \
                                -e GOOS=darwin \
                                -e GOARCH=arm64 \
                                casjaysdev/go:latest \
                                go build -buildvcs=false -trimpath -ldflags "${LDFLAGS}" -o ${BINDIR}/${PROJECT_NAME}-cli-darwin-arm64 ./src/client
                        '''
                    }
                }
                stage('CLI Windows AMD64') {
                    agent { label 'amd64' }
                    steps {
                        sh '''
                            timeout -k 30 3600 docker run --rm --no-healthcheck \
                                --name "${PROJECT_NAME}-$(head -c6 /dev/urandom | od -An -tx1 | tr -d ' \n' | cut -c1-8)" \
                                -v ${WORKSPACE}:/app \
                                -v ${GO_CACHE}:/usr/local/share/go/pkg/mod \
                                -v ${GO_BUILD}:/usr/local/share/go/cache \
                                -w /app \
                                -e CGO_ENABLED=0 \
                                -e GOOS=windows \
                                -e GOARCH=amd64 \
                                casjaysdev/go:latest \
                                go build -buildvcs=false -trimpath -ldflags "${LDFLAGS}" -o ${BINDIR}/${PROJECT_NAME}-cli-windows-amd64.exe ./src/client
                        '''
                    }
                }
                stage('CLI Windows ARM64') {
                    agent { label 'amd64' }
                    steps {
                        sh '''
                            timeout -k 30 3600 docker run --rm --no-healthcheck \
                                --name "${PROJECT_NAME}-$(head -c6 /dev/urandom | od -An -tx1 | tr -d ' \n' | cut -c1-8)" \
                                -v ${WORKSPACE}:/app \
                                -v ${GO_CACHE}:/usr/local/share/go/pkg/mod \
                                -v ${GO_BUILD}:/usr/local/share/go/cache \
                                -w /app \
                                -e CGO_ENABLED=0 \
                                -e GOOS=windows \
                                -e GOARCH=arm64 \
                                casjaysdev/go:latest \
                                go build -buildvcs=false -trimpath -ldflags "${LDFLAGS}" -o ${BINDIR}/${PROJECT_NAME}-cli-windows-arm64.exe ./src/client
                        '''
                    }
                }
                stage('CLI FreeBSD AMD64') {
                    agent { label 'amd64' }
                    steps {
                        sh '''
                            timeout -k 30 3600 docker run --rm --no-healthcheck \
                                --name "${PROJECT_NAME}-$(head -c6 /dev/urandom | od -An -tx1 | tr -d ' \n' | cut -c1-8)" \
                                -v ${WORKSPACE}:/app \
                                -v ${GO_CACHE}:/usr/local/share/go/pkg/mod \
                                -v ${GO_BUILD}:/usr/local/share/go/cache \
                                -w /app \
                                -e CGO_ENABLED=0 \
                                -e GOOS=freebsd \
                                -e GOARCH=amd64 \
                                casjaysdev/go:latest \
                                go build -buildvcs=false -trimpath -ldflags "${LDFLAGS}" -o ${BINDIR}/${PROJECT_NAME}-cli-freebsd-amd64 ./src/client
                        '''
                    }
                }
                stage('CLI FreeBSD ARM64') {
                    agent { label 'amd64' }
                    steps {
                        sh '''
                            timeout -k 30 3600 docker run --rm --no-healthcheck \
                                --name "${PROJECT_NAME}-$(head -c6 /dev/urandom | od -An -tx1 | tr -d ' \n' | cut -c1-8)" \
                                -v ${WORKSPACE}:/app \
                                -v ${GO_CACHE}:/usr/local/share/go/pkg/mod \
                                -v ${GO_BUILD}:/usr/local/share/go/cache \
                                -w /app \
                                -e CGO_ENABLED=0 \
                                -e GOOS=freebsd \
                                -e GOARCH=arm64 \
                                casjaysdev/go:latest \
                                go build -buildvcs=false -trimpath -ldflags "${LDFLAGS}" -o ${BINDIR}/${PROJECT_NAME}-cli-freebsd-arm64 ./src/client
                        '''
                    }
                }
            }
        }

        // Optional GUI client: separate dynamically linked artifact, Linux/macOS/Windows only, never BSD
        stage('Build GUI client') {
            when { expression { fileExists('src/client/gui') } }
            agent { label 'amd64' }
            steps {
                sh '''
                    for t in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64; do
                        os=${t%/*}; arch=${t#*/}; ext=""; [ "$os" = windows ] && ext=".exe"
                        timeout -k 30 3600 docker run --rm --no-healthcheck --name "${PROJECT_NAME}-$(head -c6 /dev/urandom | od -An -tx1 | tr -d ' \n' | cut -c1-8)" -v ${WORKSPACE}:/app -v ${GO_CACHE}:/usr/local/share/go/pkg/mod -v ${GO_BUILD}:/usr/local/share/go/cache -w /app -e CGO_ENABLED=0 -e GOOS=$os -e GOARCH=$arch casjaysdev/go:latest \
                            go build -buildvcs=false -trimpath -tags gui -ldflags "${LDFLAGS}" -o ${BINDIR}/${PROJECT_NAME}-cli-gui-${os}-${arch}${ext} ./src/client
                    done
                '''
            }
        }

        stage('Lint') {
            agent { label 'amd64' }
            steps {
                sh '''
                    timeout -k 30 3600 docker run --rm \
                        --no-healthcheck \
                        --name "${PROJECT_NAME}-$(head -c6 /dev/urandom | od -An -tx1 | tr -d ' \n' | cut -c1-8)" \
                        -v ${WORKSPACE}:/app \
                        -v ${GO_CACHE}:/usr/local/share/go/pkg/mod \
                        -v ${GO_BUILD}:/usr/local/share/go/cache \
                        -w /app \
                        casjaysdev/go:latest \
                        sh -c 'go vet ./... && staticcheck ./...'
                '''
            }
        }

        stage('Test') {
            agent { label 'amd64' }
            steps {
                sh '''
                    mkdir -p "/tmp/${PROJECT_ORG}"
                    COVDIR=$(mktemp -d "/tmp/${PROJECT_ORG}/${PROJECT_NAME}-XXXXXX")
                    trap 'rm -rf -- "$COVDIR"' EXIT
                    timeout -k 30 3600 docker run --rm --no-healthcheck --name "${PROJECT_NAME}-$(head -c6 /dev/urandom | od -An -tx1 | tr -d ' \n' | cut -c1-8)" -v ${WORKSPACE}:/app -v ${COVDIR}:/cov -v ${GO_CACHE}:/usr/local/share/go/pkg/mod -v ${GO_BUILD}:/usr/local/share/go/cache -w /app casjaysdev/go:latest sh -c 'mkdir -p /cov/data && go test -v -cover ./... -args -test.gocoverdir=/cov/data && go tool covdata textfmt -i=/cov/data -o=/cov/coverage.out'
                    PCT=$(timeout -k 30 3600 docker run --rm --no-healthcheck --name "${PROJECT_NAME}-$(head -c6 /dev/urandom | od -An -tx1 | tr -d ' \n' | cut -c1-8)" -v ${WORKSPACE}:/app -v ${COVDIR}:/cov -w /app casjaysdev/go:latest go tool cover -func=/cov/coverage.out | awk '/^total:/ {gsub("%","",$3); print int($3)}')
                    rm -rf "$COVDIR"
                    [ "$PCT" -ge 60 ] || { echo "coverage ${PCT}% < 60%"; exit 1; }
                '''
            }
        }

        // Security - matches the ci.yml security jobs (secret scan, workflow policy, vulnerability scan, image scan)
        stage('Security') {
            parallel {
                stage('Secret scan') {
                    agent { label 'amd64' }
                    steps {
                        sh '''
                            timeout -k 30 3600 docker run --rm --name "${PROJECT_NAME}-$(head -c6 /dev/urandom | od -An -tx1 | tr -d ' \n' | cut -c1-8)" -v ${WORKSPACE}:/repo -w /repo trufflesecurity/trufflehog@sha256:9c7a8325d43a4af2960d3f21d61a08981d8a1bf0542e007efa2e47097e386452 \
                                git file://. --results=verified --exclude-paths=.github/trufflehog-exclude.txt --fail
                        '''
                    }
                }
                stage('Workflow policy') {
                    agent { label 'amd64' }
                    steps {
                        sh '''
                            bad=$(grep -RnE '^[[:space:]]*uses:' .github/ .gitea/ .forgejo/ 2>/dev/null | grep -vE '@[0-9a-f]{40}([[:space:]]|$)' || true)
                            if [ -n "$bad" ]; then echo "unpinned actions found:"; echo "$bad"; exit 1; fi
                            unsafe=$(grep -RnE '^[[:space:]]*pull_request_target:' .github/ .gitea/ .forgejo/ 2>/dev/null || true)
                            if [ -n "$unsafe" ]; then echo "unsafe pull_request_target trigger found:"; echo "$unsafe"; exit 1; fi
                        '''
                    }
                }
                stage('Vulnerability scan') {
                    when { expression { fileExists('go.sum') } }
                    agent { label 'amd64' }
                    steps {
                        sh '''
                            timeout -k 30 3600 docker run --rm \
                                --no-healthcheck \
                                --name "${PROJECT_NAME}-$(head -c6 /dev/urandom | od -An -tx1 | tr -d ' \n' | cut -c1-8)" \
                                -v ${WORKSPACE}:/app \
                                -v ${GO_CACHE}:/usr/local/share/go/pkg/mod \
                                -v ${GO_BUILD}:/usr/local/share/go/cache \
                                -w /app \
                                casjaysdev/go:latest \
                                govulncheck ./...
                        '''
                    }
                }
                stage('Image scan') {
                    when { expression { fileExists('docker/Dockerfile') } }
                    agent { label 'amd64' }
                    steps {
                        sh '''
                            timeout -k 30 3600 docker build -t local/scan-target:ci -f docker/Dockerfile .
                            timeout -k 30 3600 bash docker/verify-image.sh local/scan-target:ci
                            timeout -k 30 3600 docker run --rm --name "${PROJECT_NAME}-$(head -c6 /dev/urandom | od -An -tx1 | tr -d ' \n' | cut -c1-8)" -v /var/run/docker.sock:/var/run/docker.sock aquasec/trivy@sha256:8f6bb947dbc528902e11f2f5be52f7b519f6a39d5d148f41166f2040499768fb \
                                image --exit-code 1 --severity CRITICAL,HIGH local/scan-target:ci
                        '''
                    }
                    post {
                        always {
                            sh 'docker rmi local/scan-target:ci || true'
                        }
                    }
                }
            }
        }

        // Stable Release - matches release.yml (tag push only)
        stage('Release: Stable') {
            agent { label 'amd64' }
            when {
                expression { env.BUILD_TYPE == 'release' }
            }
            steps {
                sh '''
                    echo "${VERSION}" > ${RELDIR}/version.txt

                    for f in ${BINDIR}/${PROJECT_NAME}-*; do
                        [ -f "$f" ] || continue
                        cp "$f" ${RELDIR}/
                    done

                    tar --exclude='.git' --exclude='.github' --exclude='.gitea' \
                        --exclude='.forgejo' --exclude='binaries' --exclude='releases' \
                        --exclude='*.tar.gz' \
                        -czf ${RELDIR}/${PROJECT_NAME}-${VERSION}-source.tar.gz .
                '''
                // SBOM tooling lives in the build image - never install it on the agent
                sh '''
                    timeout -k 30 3600 docker run --rm --no-healthcheck \
                        --name "${PROJECT_NAME}-$(head -c6 /dev/urandom | od -An -tx1 | tr -d ' \n' | cut -c1-8)" \
                        -v ${WORKSPACE}:/app \
                        -w /app \
                        -e GOFLAGS=-buildvcs=false \
                        casjaysdev/go:latest \
                        cyclonedx-gomod mod -json -output "${RELDIR}/${PROJECT_NAME}-${VERSION}-sbom.cdx.json"
                '''
                sh '''
                    cd ${RELDIR}
                    FILES="$(ls)"
                    sha256sum $FILES > sha256.txt
                    sha512sum $FILES > sha512.txt
                '''
                archiveArtifacts artifacts: 'releases/*', fingerprint: true
            }
        }

        // Beta Release - matches beta.yml (*-beta tag only)
        stage('Release: Beta') {
            agent { label 'amd64' }
            when {
                expression { env.BUILD_TYPE == 'beta' }
            }
            steps {
                sh '''
                    echo "${VERSION}" > ${RELDIR}/version.txt

                    for f in ${BINDIR}/${PROJECT_NAME}-*; do
                        [ -f "$f" ] || continue
                        cp "$f" ${RELDIR}/
                    done

                    tar --exclude='.git' --exclude='.github' --exclude='.gitea' \
                        --exclude='.forgejo' --exclude='binaries' --exclude='releases' \
                        --exclude='*.tar.gz' \
                        -czf ${RELDIR}/${PROJECT_NAME}-${VERSION}-source.tar.gz .
                '''
                // SBOM tooling lives in the build image - never install it on the agent
                sh '''
                    timeout -k 30 3600 docker run --rm --no-healthcheck \
                        --name "${PROJECT_NAME}-$(head -c6 /dev/urandom | od -An -tx1 | tr -d ' \n' | cut -c1-8)" \
                        -v ${WORKSPACE}:/app \
                        -w /app \
                        -e GOFLAGS=-buildvcs=false \
                        casjaysdev/go:latest \
                        cyclonedx-gomod mod -json -output "${RELDIR}/${PROJECT_NAME}-${VERSION}-sbom.cdx.json"
                '''
                sh '''
                    cd ${RELDIR}
                    FILES="$(ls)"
                    sha256sum $FILES > sha256.txt
                    sha512sum $FILES > sha512.txt
                '''
                archiveArtifacts artifacts: 'releases/*', fingerprint: true
            }
        }

        // Devel Release - matches devel.yml (main + scheduled)
        stage('Release: Devel') {
            agent { label 'amd64' }
            when {
                expression { env.BUILD_TYPE == 'devel' }
            }
            steps {
                sh '''
                    echo "${VERSION}" > ${RELDIR}/version.txt

                    for f in ${BINDIR}/${PROJECT_NAME}-*; do
                        [ -f "$f" ] || continue
                        cp "$f" ${RELDIR}/
                    done

                    tar --exclude='.git' --exclude='.github' --exclude='.gitea' \
                        --exclude='.forgejo' --exclude='binaries' --exclude='releases' \
                        --exclude='*.tar.gz' \
                        -czf ${RELDIR}/${PROJECT_NAME}-${VERSION}-source.tar.gz .
                '''
                // SBOM tooling lives in the build image - never install it on the agent
                sh '''
                    timeout -k 30 3600 docker run --rm --no-healthcheck \
                        --name "${PROJECT_NAME}-$(head -c6 /dev/urandom | od -An -tx1 | tr -d ' \n' | cut -c1-8)" \
                        -v ${WORKSPACE}:/app \
                        -w /app \
                        -e GOFLAGS=-buildvcs=false \
                        casjaysdev/go:latest \
                        cyclonedx-gomod mod -json -output "${RELDIR}/${PROJECT_NAME}-${VERSION}-sbom.cdx.json"
                '''
                sh '''
                    cd ${RELDIR}
                    FILES="$(ls)"
                    sha256sum $FILES > sha256.txt
                    sha512sum $FILES > sha512.txt
                '''
                archiveArtifacts artifacts: 'releases/*', fingerprint: true
            }
        }

        // Docker - matches docker.yml (main, version tags and *-beta tags)
        stage('Docker') {
            when {
                allOf {
                    expression { env.BUILD_TYPE in ['release', 'beta', 'devel'] }
                    not { changeRequest() }
                }
            }
            agent { label 'amd64' }
            steps {
                script {
                    def tags = "-t ${REGISTRY}:${COMMIT_ID}"

                    if (env.BUILD_TYPE == 'release') {
                        // Release tag - version, latest, YYMM
                        def yymm = new Date().format('yyMM')
                        tags += " -t ${REGISTRY}:${VERSION}"
                        tags += " -t ${REGISTRY}:latest"
                        tags += " -t ${REGISTRY}:${yymm}"
                    } else if (env.BUILD_TYPE == 'beta') {
                        // *-beta tag - beta
                        tags += " -t ${REGISTRY}:beta"
                    }
                    // else: all other branches get only the {commit_id} tag
                    // above — :devel is built separately by the "Docker: Devel"
                    // stage below, never by this stage

                    // Login to container registry
                    // Works with: ghcr.io, registry.gitlab.com, gitea/forgejo, docker.io
                    withCredentials([string(credentialsId: env.GIT_TOKEN_CREDENTIALS, variable: 'GIT_TOKEN')]) {
                        sh """
                            printf '%s' "\${GIT_TOKEN}" | docker login ${REGISTRY.split('/')[0]} -u ${PROJECT_ORG} --password-stdin
                        """
                    }

                    // Build multi-arch with OCI labels and manifest annotations
                    sh """
                        docker buildx create --name multiarch-builder --use 2>/dev/null || docker buildx use multiarch-builder
                        timeout -k 30 3600 docker buildx build \
                            -f docker/Dockerfile \
                            --platform linux/amd64,linux/arm64 \
                            --build-arg VERSION="${VERSION}" \
                            --build-arg COMMIT_ID="${COMMIT_ID}" \
                            --build-arg OFFICIAL_SITE="${OFFICIAL_SITE}" \
                            --build-arg BUILD_DATE="${BUILD_DATE}" \
                            --build-arg BUILD_EPOCH="${BUILD_EPOCH}" \
                            --label "org.opencontainers.image.vendor=${PROJECT_ORG}" \
                            --label "org.opencontainers.image.authors=${PROJECT_ORG}" \
                            --label "org.opencontainers.image.title=${PROJECT_NAME}" \
                            --label "org.opencontainers.image.base.name=${PROJECT_NAME}" \
                            --label "org.opencontainers.image.description=${PROJECT_NAME} - standard image (alpine)" \
                            --label "org.opencontainers.image.licenses=MIT" \
                            --label "org.opencontainers.image.version=${VERSION}" \
                            --label "org.opencontainers.image.created=${BUILD_DATE}" \
                            --label "org.opencontainers.image.revision=${COMMIT_ID}" \
                            --label "org.opencontainers.image.url=https://${GIT_FQDN}/${PROJECT_ORG}/${INTERNAL_NAME}" \
                            --label "org.opencontainers.image.source=https://${GIT_FQDN}/${PROJECT_ORG}/${INTERNAL_NAME}" \
                            --label "org.opencontainers.image.documentation=https://${GIT_FQDN}/${PROJECT_ORG}/${INTERNAL_NAME}" \
                            --annotation "manifest:org.opencontainers.image.vendor=${PROJECT_ORG}" \
                            --annotation "manifest:org.opencontainers.image.authors=${PROJECT_ORG}" \
                            --annotation "manifest:org.opencontainers.image.title=${PROJECT_NAME}" \
                            --annotation "manifest:org.opencontainers.image.base.name=${PROJECT_NAME}" \
                            --annotation "manifest:org.opencontainers.image.description=${PROJECT_NAME} - standard image (alpine)" \
                            --annotation "manifest:org.opencontainers.image.licenses=MIT" \
                            --annotation "manifest:org.opencontainers.image.version=${VERSION}" \
                            --annotation "manifest:org.opencontainers.image.created=${BUILD_DATE}" \
                            --annotation "manifest:org.opencontainers.image.revision=${COMMIT_ID}" \
                            --annotation "manifest:org.opencontainers.image.url=https://${GIT_FQDN}/${PROJECT_ORG}/${INTERNAL_NAME}" \
                            --annotation "manifest:org.opencontainers.image.source=https://${GIT_FQDN}/${PROJECT_ORG}/${INTERNAL_NAME}" \
                            --annotation "manifest:org.opencontainers.image.documentation=https://${GIT_FQDN}/${PROJECT_ORG}/${INTERNAL_NAME}" \
                            ${tags} \
                            --push \
                            .
                    """
                    sh """
                        timeout -k 30 3600 docker pull ${REGISTRY}:${COMMIT_ID}
                        REQUIRE_OVERLAY=${BUILD_TYPE == 'release' ? '1' : '0'} timeout -k 30 3600 bash docker/verify-image.sh ${REGISTRY}:${COMMIT_ID}
                    """
                }
            }
        }

        // Docker Devel - matches docker.yml build-devel job (main push + daily cron, never the main Docker stage)
        stage('Docker: Devel') {
            agent { label 'amd64' }
            when {
                allOf {
                    branch 'main'
                    not { changeRequest() }
                }
            }
            steps {
                script {
                    // Runs on every push to main (this pipeline's global cron trigger above
                    // also fires it devel) and always builds Dockerfile.dev with a
                    // single static :devel tag - MODE=development is baked into
                    // Dockerfile.dev via ENV, no extra build-arg needed here.
                    withCredentials([string(credentialsId: env.GIT_TOKEN_CREDENTIALS, variable: 'GIT_TOKEN')]) {
                        sh """
                            printf '%s' "\${GIT_TOKEN}" | docker login ${REGISTRY.split('/')[0]} -u ${PROJECT_ORG} --password-stdin
                        """
                    }

                    sh """
                        docker buildx create --name multiarch-builder --use 2>/dev/null || docker buildx use multiarch-builder
                        timeout -k 30 3600 docker buildx build \
                            -f docker/Dockerfile.dev \
                            --platform linux/amd64,linux/arm64 \
                            --build-arg COMMIT_ID="${COMMIT_ID}" \
                            --build-arg OFFICIAL_SITE="${OFFICIAL_SITE}" \
                            --build-arg BUILD_DATE="${BUILD_DATE}" \
                            --build-arg BUILD_EPOCH="${BUILD_EPOCH}" \
                            --label "org.opencontainers.image.vendor=${PROJECT_ORG}" \
                            --label "org.opencontainers.image.authors=${PROJECT_ORG}" \
                            --label "org.opencontainers.image.title=${PROJECT_NAME}" \
                            --label "org.opencontainers.image.base.name=${PROJECT_NAME}" \
                            --label "org.opencontainers.image.description=${PROJECT_NAME} - development image (alpine, debug mode)" \
                            --label "org.opencontainers.image.licenses=MIT" \
                            --label "org.opencontainers.image.created=${BUILD_DATE}" \
                            --label "org.opencontainers.image.revision=${COMMIT_ID}" \
                            --label "org.opencontainers.image.url=https://${GIT_FQDN}/${PROJECT_ORG}/${INTERNAL_NAME}" \
                            --label "org.opencontainers.image.source=https://${GIT_FQDN}/${PROJECT_ORG}/${INTERNAL_NAME}" \
                            --label "org.opencontainers.image.documentation=https://${GIT_FQDN}/${PROJECT_ORG}/${INTERNAL_NAME}" \
                            --annotation "manifest:org.opencontainers.image.vendor=${PROJECT_ORG}" \
                            --annotation "manifest:org.opencontainers.image.authors=${PROJECT_ORG}" \
                            --annotation "manifest:org.opencontainers.image.title=${PROJECT_NAME}" \
                            --annotation "manifest:org.opencontainers.image.base.name=${PROJECT_NAME}" \
                            --annotation "manifest:org.opencontainers.image.description=${PROJECT_NAME} - development image (alpine, debug mode)" \
                            --annotation "manifest:org.opencontainers.image.licenses=MIT" \
                            --annotation "manifest:org.opencontainers.image.created=${BUILD_DATE}" \
                            --annotation "manifest:org.opencontainers.image.revision=${COMMIT_ID}" \
                            --annotation "manifest:org.opencontainers.image.url=https://${GIT_FQDN}/${PROJECT_ORG}/${INTERNAL_NAME}" \
                            --annotation "manifest:org.opencontainers.image.source=https://${GIT_FQDN}/${PROJECT_ORG}/${INTERNAL_NAME}" \
                            --annotation "manifest:org.opencontainers.image.documentation=https://${GIT_FQDN}/${PROJECT_ORG}/${INTERNAL_NAME}" \
                            -t ${REGISTRY}:devel \
                            --push \
                            .
                    """
                    sh """
                        timeout -k 30 3600 docker pull ${REGISTRY}:devel
                        timeout -k 30 3600 bash docker/verify-image.sh ${REGISTRY}:devel
                    """
                }
            }
        }

    }

    post {
        always {
            cleanWs()
        }
    }
}
