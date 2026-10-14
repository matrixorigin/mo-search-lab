#!/usr/bin/env bash
# Download and install the complete offline MO Search Lab distribution.
set -euo pipefail

version='v0.14.0'
name="mo-search-lab-${version}-linux-amd64"
archive="${name}-full.tar.gz"
base_url="https://github.com/matrixorigin/mo-search-lab/releases/download/${version}"
parent="$PWD"
cache="$PWD/.mo-search-lab-downloads/$version"
offline=false
cache_explicit=false
while (($#)); do
  case "$1" in
    --offline) offline=true; shift ;;
    --base-url) base_url=${2:?missing URL}; shift 2 ;;
    --download-dir) cache=${2:?missing directory}; cache_explicit=true; shift 2 ;;
    -h|--help)
      echo "用法：bash setup.sh [安装父目录] [--offline]"
      echo "自动下载完整资源、校验并解压；断点后重新执行即可。"
      echo "离线时将 setup.sh 和本次 Release 的全部附件放在一起，追加 --offline。"
      exit 0 ;;
    --*) echo "未知参数：$1" >&2; exit 2 ;;
    *) parent=$1; shift ;;
  esac
done
if "$offline" && ! "$cache_explicit"; then cache=$(cd -- "$(dirname -- "$0")" && pwd); fi
for command in tar gzip sha256sum mktemp; do
  command -v "$command" >/dev/null || { echo "缺少命令：$command" >&2; exit 1; }
done
if [[ $(uname -s) != Linux || $(uname -m) != x86_64 ]]; then
  echo "此资源包适用于 Linux x86_64；请在运行测试的 Linux 主机上执行。" >&2
  exit 1
fi
mkdir -p -- "$parent" "$cache"
parent=$(cd -- "$parent" && pwd)
cache=$(cd -- "$cache" && pwd)
target="$parent/$name"
stage=''
cleanup() { if [[ -n "$stage" ]]; then rm -rf -- "$stage"; fi; }
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

fetch_metadata() {
  local file=$1
  if "$offline"; then
    [[ -f "$cache/$file" ]] || { echo "离线附件缺失：$file" >&2; exit 1; }
  else
    command -v curl >/dev/null || { echo '下载需要 curl；也可使用 --offline。' >&2; exit 1; }
    curl --fail --location --retry 3 --silent --show-error \
      --output "$cache/$file.tmp" "$base_url/$file"
    mv -- "$cache/$file.tmp" "$cache/$file"
  fi
}
valid_hash() {
  local file=$1 digest=$2
  [[ -f "$cache/$file" ]] && printf '%s  %s\n' "$digest" "$file" | (cd -- "$cache" && sha256sum --check --status -)
}
fetch_verified() {
  local file=$1 digest=$2
  if valid_hash "$file" "$digest"; then return; fi
  if "$offline"; then echo "离线附件缺失或校验失败：$file" >&2; exit 1; fi
  if valid_hash "$file.partial" "$digest"; then
    mv -- "$cache/$file.partial" "$cache/$file"; return
  fi
  if ! curl --fail --location --retry 3 --continue-at - --progress-bar \
    --output "$cache/$file.partial" "$base_url/$file"; then
    echo "$file 无法续传，重新下载。" >&2
    rm -f -- "$cache/$file.partial"
    curl --fail --location --retry 3 --progress-bar \
      --output "$cache/$file.partial" "$base_url/$file"
  fi
  if ! valid_hash "$file.partial" "$digest"; then
    echo "$file 校验未通过，重新下载一次。" >&2
    rm -f -- "$cache/$file.partial"
    curl --fail --location --retry 3 --progress-bar \
      --output "$cache/$file.partial" "$base_url/$file"
    valid_hash "$file.partial" "$digest" || { echo "校验失败：$file" >&2; exit 1; }
  fi
  mv -- "$cache/$file.partial" "$cache/$file"
}

fetch_metadata SHA256SUMS
parts=()
digests=()
archive_metadata=false
while read -r digest file extra; do
  [[ "$digest" =~ ^[0-9a-f]{64}$ && -z "${extra:-}" ]] || { echo '校验清单格式不正确。' >&2; exit 1; }
  if [[ "$file" == archive.sha256 ]]; then
    [[ "$archive_metadata" == false ]] || { echo '校验清单重复。' >&2; exit 1; }
    fetch_verified "$file" "$digest"
    archive_metadata=true
  elif [[ "$file" == "$archive".part[0-9][0-9] ]]; then
    expected=$(printf '%s.part%02d' "$archive" "$((${#parts[@]}+1))")
    [[ "$file" == "$expected" ]] || { echo '分卷缺失或顺序错误。' >&2; exit 1; }
    parts+=("$cache/$file")
    digests+=("$digest")
  else
    echo "校验清单包含未知文件：$file" >&2; exit 1
  fi
done < "$cache/SHA256SUMS"
[[ "$archive_metadata" == true && ${#parts[@]} -gt 0 ]] || { echo '资源清单不完整。' >&2; exit 1; }
read -r expected_hash expected_archive extra < "$cache/archive.sha256"
[[ "$expected_hash" =~ ^[0-9a-f]{64}$ && "$expected_archive" == "$archive" && -z "${extra:-}" ]] || { echo '整包校验信息不正确。' >&2; exit 1; }
if [[ -e "$target" ]]; then
  if [[ -f "$target/.bundle.sha256" && $(cat "$target/.bundle.sha256") == "$expected_hash" && -x "$target/mo-search-lab" ]]; then
    echo "已安装：$target（已有数据和报告保留）"
    exit 0
  fi
  echo "目录已存在，未覆盖：$target；可指定另一个安装父目录。" >&2
  exit 1
fi
for index in "${!parts[@]}"; do
  echo "[1/3] 准备资源 $((index+1))/${#parts[@]}：${parts[index]##*/}"
  fetch_verified "${parts[index]##*/}" "${digests[index]}"
done
echo '[2/3] 校验完整资源包'
actual=$(cat -- "${parts[@]}" | sha256sum)
[[ "${actual%% *}" == "$expected_hash" ]] || { echo '完整资源包校验失败。' >&2; exit 1; }
echo '[3/3] 解压工具与全部测试数据'
stage=$(mktemp -d "$parent/.mo-search-lab-install-XXXXXX")
cat -- "${parts[@]}" | tar --extract --gzip --no-same-owner --file=- --directory="$stage"
[[ -x "$stage/$name/mo-search-lab" && -d "$stage/$name/packs" ]] || { echo '资源包结构不完整。' >&2; exit 1; }
(cd -- "$stage/$name" && sha256sum --check --status SHA256SUMS)
printf '%s\n' "$expected_hash" > "$stage/$name/.bundle.sha256"
mv -- "$stage/$name" "$target"
echo "安装完成：$target"
echo "下一步：cd '$target'"
echo "./mo-search-lab ui --launch --reports reports --host MO_SQL_HOST --password '你的密码'"
