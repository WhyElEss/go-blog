// Post pages: images open in a gallery (PhotoSwipe 5.4.4, MIT, in
// static/photoswipe) to page through all of the post's pictures. The
// blog marks them up as links to the full copy, so without scripts a click
// just opens the image.
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
