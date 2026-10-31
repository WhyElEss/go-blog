// Public pages: the back-to-top button and, on phones, the header that
// slides away while scrolling down. Without scripts the header just stays
// at the top and there is no button.
(function () {
  "use strict";

  const reduce = window.matchMedia("(prefers-reduced-motion: reduce)");

  // The button shows once the sentinel, one screen tall at the top of the
  // page, has scrolled out of view.
  const button = document.querySelector(".to-top");
  const sentinel = document.querySelector(".top-sentinel");
  if (button && sentinel && "IntersectionObserver" in window) {
    button.hidden = false;
    new IntersectionObserver(([e]) => button.classList.toggle("shown", !e.isIntersecting)).observe(sentinel);
    button.addEventListener("click", () => {
      window.scrollTo({ top: 0, behavior: reduce.matches ? "auto" : "smooth" });
    });
  }

  // Phones only: the two-row header leaves on the way down, returns on the
  // way up.
  const head = document.querySelector(".site-head");
  const phone = window.matchMedia("(max-width: 559px)");
  if (!head) return;
  let lastY = window.scrollY;
  let ticking = false;
  function update() {
    ticking = false;
    const y = window.scrollY;
    if (!phone.matches || y < head.offsetHeight) {
      head.classList.remove("away");
    } else if (y > lastY + 6) {
      head.classList.add("away");
    } else if (y < lastY - 6) {
      head.classList.remove("away");
    } else {
      return;
    }
    lastY = y;
  }
  window.addEventListener(
    "scroll",
    () => {
      if (!ticking) {
        ticking = true;
        requestAnimationFrame(update);
      }
    },
    { passive: true }
  );
  // Keyboard users reach the menu even when it is away.
  head.addEventListener("focusin", () => head.classList.remove("away"));
})();
