// Draws the 2FA setup's QR code from data-qr with qrcode-generator
// (static/qrcode.js, v1.5.2, MIT). Black on white whatever the theme:
// scanners read an inverted code badly. A wrong picture cannot turn 2FA on:
// that takes a code matching the secret the server holds.
(function () {
  "use strict";
  const box = document.querySelector("[data-qr]");
  if (!box || typeof qrcode !== "function") return;
  const qr = qrcode(0, "M");
  qr.addData(box.dataset.qr);
  qr.make();
  const n = qr.getModuleCount();
  const quiet = 4;
  let d = "";
  for (let r = 0; r < n; r++) {
    for (let c = 0; c < n; c++) {
      if (qr.isDark(r, c)) d += "M" + c + " " + r + "h1v1h-1z";
    }
  }
  const ns = "http://www.w3.org/2000/svg";
  const svg = document.createElementNS(ns, "svg");
  const span = n + quiet * 2;
  svg.setAttribute("viewBox", -quiet + " " + -quiet + " " + span + " " + span);
  svg.setAttribute("shape-rendering", "crispEdges");
  svg.setAttribute("role", "img");
  svg.setAttribute("aria-label", "QR code");
  const bg = document.createElementNS(ns, "rect");
  bg.setAttribute("x", -quiet);
  bg.setAttribute("y", -quiet);
  bg.setAttribute("width", span);
  bg.setAttribute("height", span);
  bg.setAttribute("fill", "#fff");
  const path = document.createElementNS(ns, "path");
  path.setAttribute("d", d);
  path.setAttribute("fill", "#000");
  svg.append(bg, path);
  box.append(svg);
})();
