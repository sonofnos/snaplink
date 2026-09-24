// Runs synchronously in <head> so the saved theme is applied before first paint.
(function () {
  var t = 'system';
  try { t = localStorage.getItem('snaplink-theme') || 'system'; } catch (e) {}
  document.documentElement.setAttribute('data-theme', t);
  document.documentElement.classList.add('js');
})();
