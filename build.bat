@echo off
rem KeRemotePlay derleme betigi (Go 1.24+ gerekir)
cd /d "%~dp0"
go mod tidy || exit /b 1
go test ./... || exit /b 1
go build -trimpath -ldflags "-s -w -H windowsgui" -o dist\KeRemotePlay.exe . || exit /b 1
echo Tamam: dist\KeRemotePlay.exe
