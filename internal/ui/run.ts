// The fixed script behind `loadtool ui` (ADR-023). Everything it sends
// comes from the controller, as JSON in __ENV: the form never becomes
// code. It never throws, so request URLs and errors stay out of the
// script-error report.
import http from "loadtool/http";
import { check } from "loadtool";

const cfg = JSON.parse(__ENV.LOADTOOL_UI_RUN);
const headers = JSON.parse(__ENV.LOADTOOL_UI_HEADERS);

export const options = { thresholds: cfg.thresholds };

export default function (): void {
  const res = http.request(cfg.method, cfg.url, cfg.body === "" ? undefined : cfg.body, { headers });
  check(res, { [cfg.checkName]: (r) => r.status >= cfg.minStatus && r.status <= cfg.maxStatus });
}
