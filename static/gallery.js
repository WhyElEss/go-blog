// Post pages: images open in a gallery (PhotoSwipe 5.4.4, MIT, in
// static/photoswipe) to page through all of the post's pictures. The
// blog marks them up as links to the full copy, so without scripts a click
// just opens the image.

// Phones (content.css, under 560 px): gallery tiles wrap by themselves, and
// a filler after them (.gallery::after) keeps the last line's tiles at
// their basis width. That is right after lines of small tiles, wrong after
// lines of full-width pictures, where the last picture should fill its line
// too. So the last line fills (class fill-last drops the filler) when it
// would then be at most 1.5 times as tall as the tallest other line: the
// proportion of galleryRowMax (270 px) to the 180 px row aim on wide
// screens (markdown.go).
const phone = matchMedia("(max-width: 559px)");
const fitLastLines = () => {
  for (const g of document.querySelectorAll(".content .gallery")) {
    g.classList.remove("fill-last");
    if (!phone.matches) continue;
    const tiles = [...g.querySelectorAll(".gi")];
    const top = tiles.at(-1)?.offsetTop;
    const last = tiles.filter((t) => t.offsetTop === top);
    const others = tiles.filter((t) => t.offsetTop !== top);
    if (!others.length) continue;
    const tallest = Math.max(...others.map((t) => t.offsetHeight));
    const n = last.reduce((s, t) => s + Number(getComputedStyle(t).getPropertyValue("--n")), 0);
    const gap = parseFloat(getComputedStyle(g).columnGap) || 0;
    const height = (g.clientWidth - gap * (last.length - 1)) / (n / 20);
    if (n > 0 && height <= tallest * 1.5) g.classList.add("fill-last");
  }
};
let fitting = 0;
const refit = () => {
  cancelAnimationFrame(fitting);
  fitting = requestAnimationFrame(fitLastLines);
};
fitLastLines();
addEventListener("resize", refit);

const v = new URL(import.meta.url).search;
const { default: PhotoSwipeLightbox } = await import("./photoswipe/photoswipe-lightbox.esm.min.js" + v);

// Button titles follow the reader's language, not the post's.
const ru = document.querySelector("script[data-lang]")?.dataset.lang === "ru";
const lightbox = new PhotoSwipeLightbox({
  gallery: ".post",
  children: "a.zoom",
  pswpModule: () => import("./photoswipe/photoswipe.esm.min.js" + v),
  closeTitle: ru ? "Закрыть" : "Close",
  zoomTitle: ru ? "Увеличить" : "Zoom",
  arrowPrevTitle: ru ? "Предыдущая" : "Previous",
  arrowNextTitle: ru ? "Следующая" : "Next",
  errorMsg: ru ? "Картинка не загрузилась" : "The image cannot be loaded",
});
lightbox.init();
