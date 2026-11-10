# .NET SDK / Roslyn hard-codes OriginalFilename = <AssemblyName>.dll in the
# Win32 VERSIONINFO resource, with no MSBuild property to override it.
# Rewrite it in place after build: ".dll" and ".exe" are both 4 characters, so
# the UTF-16LE replacement keeps every offset and length in the PE intact.
#
# The binary contains the string twice (InternalName and OriginalFilename), so
# this only rewrites the value that directly follows the OriginalFilename key.
param(
    [Parameter(Mandatory = $true)][string]$Path
)

if (-not (Test-Path $Path)) { exit 0 }

$bytes = [System.IO.File]::ReadAllBytes($Path)

# Latin1 maps every byte to exactly one char, so UTF-16LE byte patterns can be
# searched as ordinal substrings while offsets stay identical to byte offsets.
$latin1 = [System.Text.Encoding]::GetEncoding(28591)
$text = $latin1.GetString($bytes)
$needle = $latin1.GetString([System.Text.Encoding]::Unicode.GetBytes('SingBoxWin.Native.dll'))
$key = $latin1.GetString([System.Text.Encoding]::Unicode.GetBytes('OriginalFilename'))
$nul = [char]0

$hits = New-Object System.Collections.Generic.List[int]
$pos = 0
while (($pos = $text.IndexOf($key, $pos, [System.StringComparison]::Ordinal)) -ge 0) {
    # VERSIONINFO pads between key and value with NULs (UTF-16 alignment).
    $value = $pos + $key.Length
    while ($value -lt $text.Length -and $text[$value] -eq $nul) { $value++ }
    if ($value + $needle.Length -le $text.Length -and $text.Substring($value, $needle.Length) -eq $needle) {
        $hits.Add($value)
    }
    $pos++
}

if ($hits.Count -eq 0) { exit 0 }
if ($hits.Count -gt 1) {
    Write-Warning "fix-original-filename: found $($hits.Count) OriginalFilename values in $Path; skipping (refusing to guess)."
    exit 0
}

# ".dll" occupies the last 8 bytes of the UTF-16LE match; overwrite with ".exe".
$offset = $hits[0] + $needle.Length - 8
$replacement = [System.Text.Encoding]::Unicode.GetBytes('.exe')
[System.Array]::Copy($replacement, 0, $bytes, $offset, $replacement.Length)

try {
    [System.IO.File]::WriteAllBytes($Path, $bytes)
} catch {
    Write-Warning "fix-original-filename: could not write $Path ($($_.Exception.Message))."
    exit 0
}

exit 0
