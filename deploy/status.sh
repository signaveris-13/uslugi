#!/usr/bin/env bash
# Сводка о здоровье сервера для агента-адвайзера.
# Запускается cron'ом раз в 10 минут (ставит update.sh), пишет deploy/status/status.txt.
# Caddy отдаёт его по секретной ссылке https://$DOMAIN/ops/$STATUS_TOKEN/status.txt.
# Токены, пароли и адреса баз из логов вырезаются.
set -uo pipefail
cd "$(dirname "$0")/.." || exit 1
ROOT=$(pwd)
OUT_DIR="$ROOT/deploy/status"
HIST="$OUT_DIR/history.tsv"
DC=(docker compose -f deploy/compose.yml --env-file deploy/.env)
SERVICES="postgres bot web worker caddy backup"
mkdir -p "$OUT_DIR"

redact() {
  sed -E \
    -e 's/[0-9]{6,}:[A-Za-z0-9_-]{30,}/<token>/g' \
    -e 's#(postgres(ql)?://[^:/@ ]+:)[^@ ]+@#\1<pass>@#g' \
    -e 's/(password|passwd|secret|token)=[^ &"]+/\1=<hidden>/Ig' \
    | cut -c1-300
}

now=$(date +%s)

# --- память, swap, нагрузка, диск
mem_total=$(awk '/^MemTotal:/{print int($2/1024)}' /proc/meminfo)
mem_avail=$(awk '/^MemAvailable:/{print int($2/1024)}' /proc/meminfo)
swap_total=$(awk '/^SwapTotal:/{print int($2/1024)}' /proc/meminfo)
swap_free=$(awk '/^SwapFree:/{print int($2/1024)}' /proc/meminfo)
swap_used=$((swap_total - swap_free))
mem_used_pct=$(( (mem_total - mem_avail) * 100 / (mem_total > 0 ? mem_total : 1) ))
read -r load1 load5 load15 _ < /proc/loadavg
cpus=$(nproc)
disk_pct=$(df -P / | awk 'NR==2{gsub("%","",$5); print $5}')
disk_free_gb=$(df -P / | awk 'NR==2{printf "%.1f", $4/1048576}')

# история за сутки: время, % памяти, swap МБ, load1, % диска
echo -e "$now\t$mem_used_pct\t$swap_used\t$load1\t$disk_pct" >> "$HIST"
tail -n 144 "$HIST" > "$HIST.tmp" && mv "$HIST.tmp" "$HIST"

{
echo "# Uslugi — состояние сервера"
echo "время: $(date -u '+%F %T UTC')"
echo "хост: $(hostname), аптайм $(uptime -p 2>/dev/null || cat /proc/uptime)"
echo "версия кода: $(git rev-parse --short HEAD 2>/dev/null) ($(git log -1 --format=%cd --date=short 2>/dev/null))"
echo

echo "## Ресурсы сейчас"
echo "CPU: $cpus ядер, load $load1 / $load5 / $load15 (1/5/15 мин)"
echo "память: занято ${mem_used_pct}% из ${mem_total} МБ, доступно ${mem_avail} МБ"
echo "swap: занято ${swap_used} из ${swap_total} МБ"
echo "диск /: занято ${disk_pct}%, свободно ${disk_free_gb} ГБ"
echo

echo "## Ресурсы за последние сутки (замер раз в 10 мин)"
awk -F'\t' '
  NR==1{mn=$2; mx=$2; smx=$3; lmx=$4; d0=$5}
  {if($2>mx)mx=$2; if($2<mn)mn=$2; if($3>smx)smx=$3; if($4+0>lmx+0)lmx=$4; s+=$2; d1=$5; n++}
  END{if(n) printf "замеров: %d\nпамять: мин %d%%, средн %d%%, макс %d%%\nswap макс: %d МБ\nload1 макс: %s\nдиск: был %s%%, стал %s%%\n", n, mn, s/n, mx, smx, lmx, d0, d1}
' "$HIST"
echo

echo "## Убийства процессов из-за нехватки памяти (ядро, 24 ч)"
oom=$(journalctl -k --since "24 hours ago" --no-pager 2>/dev/null | grep -ciE 'out of memory|oom-kill|killed process' || true)
echo "событий OOM: ${oom:-нет данных}"
echo

echo "## Контейнеры"
echo "сервис | состояние | health | перезапусков | OOMKilled | запущен | CPU | память"
declare -A stats
while IFS='|' read -r name cpu mem; do stats[$name]="$cpu | $mem"; done < <(
  docker stats --no-stream --format '{{.Name}}|{{.CPUPerc}}|{{.MemUsage}} ({{.MemPerc}})' 2>/dev/null)
for svc in $SERVICES; do
  id=$("${DC[@]}" ps -aq "$svc" 2>/dev/null | head -1)
  if [ -z "$id" ]; then echo "$svc | НЕ НАЙДЕН | - | - | - | - | - | -"; continue; fi
  info=$(docker inspect --format '{{.Name}}|{{.State.Status}}|{{if .State.Health}}{{.State.Health.Status}}{{else}}-{{end}}|{{.RestartCount}}|{{.State.OOMKilled}}|{{.State.StartedAt}}' "$id")
  IFS='|' read -r cname status health restarts oomk started <<< "$info"
  cname=${cname#/}
  echo "$svc | $status | $health | $restarts | $oomk | ${started%%.*} | ${stats[$cname]:--}"
done
echo

echo "## Ошибки в логах за 24 ч"
for svc in bot web worker postgres caddy backup; do
  logs=$("${DC[@]}" logs --since 24h --no-color --no-log-prefix "$svc" 2>&1)
  errs=$(grep -iE 'level=(error|fatal)|"level":"(error|fatal)"|panic|fatal|\berror\b|\bERROR\b' <<< "$logs" | grep -viE 'level=(info|debug)|"level":"(info|debug)"')
  cnt=$(grep -c . <<< "$errs" || true)
  [ -z "$errs" ] && cnt=0
  echo "### $svc: $cnt строк с ошибками"
  if [ "$cnt" -gt 0 ]; then
    echo "частые (до 8, с количеством):"
    redact <<< "$errs" \
      | sed -E 's/^[0-9TZ:. +-]+ //; s/time=[^ ]+ //; s/[0-9a-f]{8}-[0-9a-f-]{27}/<uuid>/g; s/[0-9]{5,}/<N>/g' \
      | sort | uniq -c | sort -rn | head -8 | sed -E 's/^ +/  /'
    echo "последние 3:"
    tail -3 <<< "$errs" | redact | sed 's/^/  /'
  fi
done
echo

echo "## Доступность снаружи"
DOMAIN=$(grep '^DOMAIN=' deploy/.env | cut -d= -f2)
hz=$(curl -s -o /dev/null -m 10 -w '%{http_code} за %{time_total}с' "https://$DOMAIN/healthz")
[[ "$hz" == 000* || -z "$hz" ]] && hz="НЕТ ОТВЕТА"

echo "https://$DOMAIN/healthz: $hz"
cert_end=$(echo | timeout 10 openssl s_client -connect "$DOMAIN:443" -servername "$DOMAIN" 2>/dev/null | openssl x509 -noout -enddate 2>/dev/null | cut -d= -f2)
[ -n "$cert_end" ] && echo "сертификат до: $cert_end (дней осталось: $(( ($(date -d "$cert_end" +%s) - now) / 86400 )))"
echo

echo "## База"
q() { "${DC[@]}" exec -T postgres psql -U uslugi -d uslugi -tAc "$1" 2>/dev/null; }
echo "размер: $(q "select pg_size_pretty(pg_database_size('uslugi'))")"
echo "соединений: $(q "select count(*) from pg_stat_activity where datname='uslugi'") из $(q "show max_connections")"
echo "долгие запросы (>1 мин): $(q "select count(*) from pg_stat_activity where state<>'idle' and now()-query_start > interval '1 minute'")"
echo "версия миграций: $(q "select max(version_id) from goose_db_version where is_applied" || echo '?')"
echo "крупные таблицы:"
q "select relname||': '||pg_size_pretty(pg_total_relation_size(relid))||', строк ~'||n_live_tup from pg_stat_user_tables order by pg_total_relation_size(relid) desc limit 5" | sed 's/^/  /'
echo

echo "## Бэкапы (deploy/backups, на этом же сервере)"
last=$(ls -1t deploy/backups/uslugi-*.sql.gz 2>/dev/null | head -1)
if [ -n "$last" ]; then
  age_h=$(( (now - $(stat -c %Y "$last")) / 3600 ))
  echo "последний дамп базы: $(basename "$last"), $(du -h "$last" | cut -f1), $age_h ч назад"
  echo "дампов всего: $(ls -1 deploy/backups/uslugi-*.sql.gz | wc -l), папка занимает $(du -sh deploy/backups | cut -f1)"
  small=$(find deploy/backups -name 'uslugi-*.sql.gz' -size -2k | wc -l)
  [ "$small" -gt 0 ] && echo "ВНИМАНИЕ: подозрительно маленьких дампов (<2 КБ): $small"
else
  echo "дампов базы НЕТ"
fi
echo "копия вне сервера: не настроена"
echo

echo "## Docker на диске"
docker system df --format '{{.Type}}: {{.Size}} (можно освободить {{.Reclaimable}})' 2>/dev/null
} > "$OUT_DIR/status.txt.tmp" 2>&1

mv "$OUT_DIR/status.txt.tmp" "$OUT_DIR/status.txt"
chmod 644 "$OUT_DIR/status.txt"
