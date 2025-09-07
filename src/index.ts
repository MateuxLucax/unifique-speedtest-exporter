import http from 'http';
import { chromium } from 'playwright';

const port = parseInt(process.env.PORT || "3000");

const server = http.createServer(async (req, res) => {
  console.log(`${req.method} - ${req.url}`);

  if (req.method !== "GET") {
    res.statusCode = 404;
    res.end("Not Found");
    return;
  }

  if (new URL(`http://${req.headers.host}${req.url}`).pathname !== "/metrics") {
    res.statusCode = 404;
    res.end("Not Found");
    return;
  }

  const browser = await chromium.launch({ headless: true });
  const page = await browser.newPage();

  try {
    await page.goto("https://speed.unifique.com.br", { waitUntil: "networkidle" });

    await page.click("#startStopBtn");

    await page.waitForSelector("#resultsImg");

    const dlText = await page.textContent("#dlText");
    const ulText = await page.textContent("#ulText");
    const pingText = await page.textContent("#pingText");
    const jitterText = await page.textContent("#jitText");

    const downloadMbps = parseFloat(dlText ?? "0");
    const uploadMbps = parseFloat(ulText ?? "0");
    const pingMs = parseFloat(pingText ?? "0");
    const jitterMs = parseFloat(jitterText ?? "0");

    const downloadBps = downloadMbps * 1_000_000;
    const uploadBps = uploadMbps * 1_000_000;

    const metrics = [
      "# HELP speed_download_bits_per_second Download speed in bits per second",
      "# TYPE speed_download_bits_per_second gauge",
      `speed_download_bits_per_second ${downloadBps}`,
      "# HELP speed_upload_bits_per_second Upload speed in bits per second",
      "# TYPE speed_upload_bits_per_second gauge",
      `speed_upload_bits_per_second ${uploadBps}`,
      "# HELP speed_ping_ms Ping in milliseconds",
      "# TYPE speed_ping_ms gauge",
      `speed_ping_ms ${pingMs}`,
      "# HELP speed_jitter_ms Jitter in milliseconds",
      "# TYPE speed_jitter_ms gauge",
      `speed_jitter_ms ${jitterMs}`,
    ].join("\n");

    res.statusCode = 200;
    res.setHeader('Content-Type', 'text/plain; version=0.0.4; charset=utf-8');
    res.end(metrics + "\n");
  } catch (err) {
    res.statusCode = 500;
    res.end("Error: " + String(err));
  } finally {
    await browser.close();
  }
});

server.listen(port, () => {
  console.log(`Server started at localhost:${port}`);
});
