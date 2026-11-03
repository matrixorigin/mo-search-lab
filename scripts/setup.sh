#!/usr/bin/env bash
# Install the current tool while reusing the immutable public-data release.
set -euo pipefail

version='v0.15.0'
data_version='v0.14.1'
data_installer_sha='b897ae948e592b76eae89676c9c0c9eb2c00eb22650f66ef54bc66ddf027ba37'
release_root='https://github.com/matrixorigin/mo-search-lab/releases/download'
base_url="$release_root/$version"
data_url="$release_root/$data_version"
name="mo-search-lab-$version-linux-amd64"
archive="$name.tar.gz"
target="$PWD/mo-search-lab"
if [[ -x "$PWD/mo-search-lab" && -d "$PWD/packs" ]]; then target="$PWD"; fi
cache="$PWD/.mo-search-lab-downloads"
offline=false
cache_explicit=false
target_explicit=false
while (($#)); do
  case "$1" in
    --offline) offline=true; shift ;;
    --base-url) base_url=${2:?missing URL}; shift 2 ;;
    --data-base-url) data_url=${2:?missing URL}; shift 2 ;;
    --download-dir) cache=${2:?missing directory}; cache_explicit=true; shift 2 ;;
    -h|--help)
      echo '用法：bash setup.sh [安装目录] [--offline]'
      echo '首次安装自动获取工具和全部数据；在已有安装目录运行时只升级工具和文档。'
      echo '离线升级：将新 Release 附件放在一起，指定已有安装目录并加 --offline。'
      exit 0 ;;
    --*) echo "未知参数：$1" >&2; exit 2 ;;
    *)
      if "$target_explicit"; then echo '只能指定一个安装目录' >&2; exit 2; fi
      target=$1; target_explicit=true; shift ;;
  esac
done
if "$offline" && ! "$cache_explicit"; then cache=$(cd -- "$(dirname -- "$0")" && pwd); fi
for command in tar gzip sha256sum mktemp awk cp mv; do
  command -v "$command" >/dev/null || { echo "缺少命令：$command" >&2; exit 1; }
done
if [[ $(uname -s) != Linux || $(uname -m) != x86_64 ]]; then
  echo '此安装包适用于 Linux x86_64。' >&2; exit 1
fi
if [[ -L "$target" ]]; then echo '安装目录不能是符号链接。' >&2; exit 1; fi
existing=false
if [[ -e "$target" ]]; then
  [[ -d "$target" && -x "$target/mo-search-lab" && -d "$target/packs" ]] || {
    echo "目标不是现有安装目录，未覆盖：$target" >&2; exit 1;
  }
  existing=true
elif "$offline"; then
  echo '离线升级需要已有安装目录；首次离线部署请携带已安装的完整目录。' >&2; exit 1
fi
mkdir -p -- "$(dirname -- "$target")" "$cache"
target="$(cd -- "$(dirname -- "$target")" && pwd)/$(basename -- "$target")"
cache=$(cd -- "$cache" && pwd)
tool_cache="$cache/$version"
if "$offline" && [[ -f "$cache/SHA256SUMS" ]]; then tool_cache=$cache; fi
mkdir -p -- "$tool_cache"
stage=$(mktemp -d "$(dirname -- "$target")/.mo-search-lab-update-XXXXXX")
updating=false
items=(README.md LICENSE NOTICE TOOL.json docs SHA256SUMS mo-search-lab)
cleanup() {
  local status=$? item
  trap - EXIT
  if "$updating"; then
    echo '升级未完成，恢复原程序和文档。' >&2
    for item in "${items[@]}"; do
      rm -rf -- "$target/$item"
      if [[ -e "$stage/backup/$item" || -L "$stage/backup/$item" ]]; then
        mv -- "$stage/backup/$item" "$target/$item" || status=1
      fi
    done
  fi
  rm -rf -- "$stage"
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

valid_hash() {
  [[ -f "$1" ]] && printf '%s  %s\n' "$2" "$1" | sha256sum --check --status -
}
fetch() {
  local url=$1 file=$2
  command -v curl >/dev/null || { echo '下载需要 curl；已有安装可以离线升级。' >&2; exit 1; }
  curl --fail --location --retry 3 --silent --show-error --output "$file.partial" "$url"
  mv -- "$file.partial" "$file"
}
if ! "$offline"; then fetch "$base_url/SHA256SUMS" "$tool_cache/SHA256SUMS"; fi
[[ -f "$tool_cache/SHA256SUMS" ]] || { echo '缺少 SHA256SUMS。' >&2; exit 1; }
digest=''
while read -r hash file extra; do
  [[ "$hash" =~ ^[0-9a-f]{64}$ && -z "${extra:-}" && ( "$file" == "$archive" || "$file" == setup.sh ) ]] || {
    echo '升级包校验清单格式不正确。' >&2; exit 1;
  }
  if [[ "$file" == "$archive" ]]; then
    [[ -z "$digest" ]] || { echo '校验清单重复。' >&2; exit 1; }
    digest=$hash
  fi
done < "$tool_cache/SHA256SUMS"
[[ -n "$digest" ]] || { echo "校验清单缺少 $archive" >&2; exit 1; }
if ! valid_hash "$tool_cache/$archive" "$digest"; then
  if "$offline"; then echo '离线升级包缺失或校验失败。' >&2; exit 1; fi
  fetch "$base_url/$archive" "$tool_cache/$archive"
  valid_hash "$tool_cache/$archive" "$digest" || { echo '工具包校验失败。' >&2; exit 1; }
fi
mkdir -- "$stage/tool"
tar --extract --gzip --no-same-owner --file="$tool_cache/$archive" --directory="$stage/tool"
tool="$stage/tool/$name"
[[ -x "$tool/mo-search-lab" && -f "$tool/TOOL.json" ]] || { echo '工具包结构不完整。' >&2; exit 1; }
(cd -- "$tool" && sha256sum --check --status SHA256SUMS)
[[ $("$tool/mo-search-lab" version) == "$version" ]] || { echo '程序版本不匹配。' >&2; exit 1; }

if "$existing"; then
  installation=$target
else
  data_cache="$cache/$data_version"
  mkdir -p -- "$data_cache"
  if ! valid_hash "$data_cache/setup.sh" "$data_installer_sha"; then
    fetch "$data_url/setup.sh" "$data_cache/setup.sh"
    valid_hash "$data_cache/setup.sh" "$data_installer_sha" || { echo '数据安装脚本校验失败。' >&2; exit 1; }
  fi
  echo "首次安装：准备 $data_version 数据资源（已缓存附件会复用）。"
  if ! bash "$data_cache/setup.sh" "$stage/data" --download-dir "$data_cache" --base-url "$data_url" > "$stage/data-install.log"; then
    cat "$stage/data-install.log" >&2; exit 1
  fi
  installation="$stage/data/mo-search-lab-$data_version-linux-amd64"
fi

# Preserve the original data-bundle identity and checksums for data metadata.
# TOOL.json identifies the current program; no packs or reports are replaced.
awk '{path=$2; sub(/^\.\//, "", path); if (path !~ /^packs\//) print}' "$tool/SHA256SUMS" > "$stage/installed.sha256"
if [[ -f "$installation/SHA256SUMS" ]]; then
  awk '{path=$2; sub(/^\.\//, "", path); if (path !~ /^(mo-search-lab$|README.md$|LICENSE$|NOTICE$|TOOL.json$|docs\/)/) print}' \
    "$installation/SHA256SUMS" >> "$stage/installed.sha256"
fi
mv -- "$stage/installed.sha256" "$tool/SHA256SUMS"
if "$existing"; then
  mkdir -- "$stage/backup"
  for item in "${items[@]}"; do
    if [[ -e "$target/$item" || -L "$target/$item" ]]; then cp -a -- "$target/$item" "$stage/backup/"; fi
  done
  updating=true
fi
for item in "${items[@]}"; do
  rm -rf -- "$installation/$item"
  mv -- "$tool/$item" "$installation/$item"
done
if ! "$existing"; then mv -- "$installation" "$target"; fi
updating=false
echo "已安装 $version：$target（数据集和历史报告保留）"
echo "下一步：cd '$target'"
echo "./mo-search-lab ui --launch --reports reports --host MO_SQL_HOST --password '你的密码'"
