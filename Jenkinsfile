pipeline {
    agent none
    options {
        disableConcurrentBuilds()
        timeout(time: 30, unit: 'MINUTES')
    }
    environment {
        CGO_ENABLED = '0'
    }
    stages {
        stage('Build') {
            when { not { buildingTag() } }
            agent { docker { image 'casjaysdev/go:latest'; args '--user 0:0' } }
            steps {
                sh 'go generate ./src/swagger && go build -buildvcs=false ./...'
            }
        }
        stage('Test') {
            when { not { buildingTag() } }
            agent { docker { image 'casjaysdev/go:latest'; args '--user 0:0' } }
            steps {
                sh 'go test -cover ./...'
            }
        }
        stage('Security') {
            parallel {
                stage('Secret Scan') {
                    agent { label 'docker' }
                    steps {
                        sh 'docker run --rm --name "shortner-trufflehog-${BUILD_NUMBER}" -v "$PWD:/repo" trufflesecurity/trufflehog:latest git file:///repo --since-commit HEAD~1 --fail'
                    }
                }
                stage('Vulnerability Scan') {
                    agent { docker { image 'casjaysdev/go:latest'; args '--user 0:0' } }
                    steps {
                        sh 'govulncheck ./...'
                    }
                }
            }
        }
        stage('Release') {
            when { buildingTag() }
            agent { docker { image 'casjaysdev/go:latest'; args '--user 0:0' } }
            steps {
                sh '''
                    mkdir -p dist
                    VERSION="${TAG_NAME#v}"
                    COMMIT_ID="$(git rev-parse --short=7 HEAD)"
                    BUILD_EPOCH="$(date -u +%s)"
                    LDFLAGS="-s -w -X 'main.Version=${VERSION}' -X 'main.CommitID=${COMMIT_ID}' -X 'main.BuildEpoch=${BUILD_EPOCH}'"
                    for pair in "linux amd64" "linux arm64" "darwin amd64" "darwin arm64" "windows amd64" "windows arm64" "freebsd amd64" "freebsd arm64"; do
                        set -- $pair
                        suffix=""
                        [ "$1" != windows ] || suffix=.exe
                        GOOS=$1 GOARCH=$2 go build -buildvcs=false -trimpath -ldflags "$LDFLAGS" -o "dist/shortner-$1-$2$suffix" ./src
                    done
                    sha256sum dist/* > dist/sha256.txt
                '''
                archiveArtifacts artifacts: 'dist/*', fingerprint: true
            }
        }
    }
}
