# Downloads printpipe into a temp folder, checks it against SHA256SUMS, runs its
# install with the origin in PRINTPIPE_ORIGIN and deletes it. Placing the binary,
# Path and everything else are up to that install.
#
#   $env:PRINTPIPE_ORIGIN="<url>"; irm https://github.com/alexgred/printpipe/releases/latest/download/install.ps1 | iex
#
# iex runs this in the user's own session: "exit" would close their window, and
# anything outside the script block would stay behind in it. So a failure sets
# $LASTEXITCODE and returns.

& {
    $ErrorActionPreference = 'Stop'
    # The progress bar makes Windows PowerShell's downloads many times slower.
    $ProgressPreference = 'SilentlyContinue'

    $releases = 'https://github.com/alexgred/printpipe/releases/latest/download'
    $asset = 'printpipe-windows-amd64.exe'
    $nothingInstalled = 'Nothing was installed.'

    function Say-Failure([string]$message, [string[]]$hints) {
        [Console]::Error.WriteLine("error: $message")
        foreach ($hint in $hints) {
            [Console]::Error.WriteLine("  $hint")
        }
        $global:LASTEXITCODE = 1
    }

    # Download is whether the file came; if not, it has said why.
    function Download([string]$name, [string]$path) {
        try {
            Invoke-WebRequest -UseBasicParsing -Uri "$releases/$name" -OutFile $path
            return $true
        } catch {
            # Windows PowerShell and PowerShell 7 fail on the network with different types.
            $network = $_.Exception.GetType().Name -in 'WebException', 'HttpRequestException'
            if ($network -and -not $_.Exception.Response) {
                Say-Failure 'could not reach GitHub' @('Check the internet connection and run the same command again.')
            } else {
                Say-Failure "could not download $name from GitHub" @('Run the same command again.')
            }
            return $false
        }
    }

    $dir = Join-Path ([IO.Path]::GetTempPath()) ('printpipe-' + [guid]::NewGuid())
    try {
        try {
            $null = New-Item -ItemType Directory -Path $dir
        } catch {
            Say-Failure 'could not make a temporary folder' @('Check that the temp folder is writable and not full, then run the same command again.')
            return
        }
        $exe = Join-Path $dir 'printpipe.exe'
        $sums = Join-Path $dir 'SHA256SUMS'
        if (-not (Download $asset $exe)) { return }
        if (-not (Download 'SHA256SUMS' $sums)) { return }

        # Each line is a hash, spaces and the name, with a * before it in binary mode.
        $want = $null
        foreach ($line in Get-Content -LiteralPath $sums) {
            $fields = -split $line
            if ($fields.Count -eq 2 -and $fields[1].TrimStart('*') -eq $asset) {
                $want = $fields[0]
                break
            }
        }
        if (-not $want) {
            Say-Failure "SHA256SUMS does not list $asset" @($nothingInstalled)
            return
        }
        if ((Get-FileHash -Algorithm SHA256 -LiteralPath $exe).Hash -ne $want) {
            Say-Failure 'the download does not match SHA256SUMS: it is damaged or was tampered with' @($nothingInstalled)
            return
        }

        $installArgs = @('install')
        if ($env:PRINTPIPE_ORIGIN) {
            $installArgs += @('--origin', $env:PRINTPIPE_ORIGIN)
        }
        & $exe @installArgs
    } finally {
        Remove-Item -LiteralPath $dir -Recurse -Force -ErrorAction SilentlyContinue
    }
}
