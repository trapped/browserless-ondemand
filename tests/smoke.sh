#!/usr/bin/env bash
set -euo pipefail
image=${1:-browserless-ondemand:test}
name=ondemand-smoke-$RANDOM
trap 'docker logs "$name"; docker rm -f "$name" >/dev/null' EXIT
docker run -d --name "$name" -e TOKEN=smoke-only -e ONDEMAND_IDLE_TIMEOUT=3s "$image"
for attempt in $(seq 1 30); do
  if docker exec "$name" curl -fsS http://localhost:3000/_ondemand/health >/dev/null; then break; fi
  sleep 1
done
check_asleep() {
  docker exec "$name" sh -c '! pgrep -x node && ! pgrep -x Xvfb && ! pgrep -x chromium'
}
check_asleep
for cycle in 1 2; do
  docker exec -i "$name" node --input-type=module <<'JS'
import puppeteer from 'puppeteer-core';
const browser = await puppeteer.connect({browserWSEndpoint:'ws://127.0.0.1:3000?token=smoke-only'});
try {
 const page = await browser.newPage();
 await page.setContent('<title>on demand</title><h1>working</h1>');
 if (await page.title() !== 'on demand') throw new Error('wrong title');
 // A live websocket must survive beyond the idle timeout.
 await new Promise(resolve=>setTimeout(resolve,5000));
 if (await page.$eval('h1', el=>el.textContent)!=='working') throw new Error('session interrupted');
 await page.screenshot({path:'/tmp/ondemand-smoke.png'});
} finally {await browser.close();}
JS
  for attempt in $(seq 1 25); do
    if check_asleep; then break; fi
    sleep 1
  done
  check_asleep
done
