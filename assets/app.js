/* Media Center — resume posisi tonton, bar kemajuan, lanjut otomatis. */
(function () {
  'use strict';
  var KEY = 'mediacenter.progress';

  function load() {
    try { return JSON.parse(localStorage.getItem(KEY) || '{}'); } catch (e) { return {}; }
  }
  function save(d) {
    try { localStorage.setItem(KEY, JSON.stringify(d)); } catch (e) {}
  }
  function pct(p) {
    if (!p || !p.d) return 0;
    return Math.max(0, Math.min(100, (p.t / p.d) * 100));
  }
  function fmt(sec) {
    sec = Math.max(0, Math.round(sec));
    var h = Math.floor(sec / 3600), m = Math.floor((sec % 3600) / 60), s = sec % 60;
    var mm = (h && m < 10 ? '0' : '') + m;
    var ss = (s < 10 ? '0' : '') + s;
    return (h ? h + ':' : '') + mm + ':' + ss;
  }

  /* ---------- halaman tonton ---------- */
  var player = document.getElementById('player');
  if (player && player.dataset.id) {
    var id = player.dataset.id, store = load(), resume = store[id] ? store[id].t : 0;

    player.addEventListener('loadedmetadata', function () {
      if (resume > 8 && resume < player.duration - 12) {
        player.currentTime = resume;
      }
    }, { once: true });

    var tick = 0;
    player.addEventListener('timeupdate', function () {
      if (++tick % 5) return;
      var all = load();
      all[id] = { t: player.currentTime, d: player.duration || 0, ts: Date.now() };
      save(all);
    });

    player.addEventListener('ended', function () {
      var all = load();
      delete all[id];
      save(all);
      if (player.dataset.next) {
        setTimeout(function () { location.href = player.dataset.next; }, 900);
      }
    });

    /* pintasan papan tuli: spasi putar/jeda, ←→ geser 5 detik */
    document.addEventListener('keydown', function (e) {
      if (e.target !== player && e.target.tagName === 'INPUT') return;
      if (e.code === 'Space') { e.preventDefault(); player.paused ? player.play() : player.pause(); }
      else if (e.code === 'ArrowLeft') { player.currentTime = Math.max(0, player.currentTime - 5); }
      else if (e.code === 'ArrowRight') { player.currentTime = Math.min(player.duration || 0, player.currentTime + 5); }
    });
  }

  /* ---------- daftar: bar kemajuan + "Lanjutkan Menonton" ---------- */
  var nodes = Array.prototype.slice.call(
    document.querySelectorAll('.card[data-id], .eplist a[data-id]')
  );
  if (!nodes.length) return;

  var all = load(), recent = [];
  nodes.forEach(function (n) {
    var id = n.getAttribute('data-id');
    var p = all[id];
    if (!p || !p.d || p.t < 15 || p.t > p.d - 15) return;
    var bar = n.querySelector('.bar i') || n.querySelector('.pbar i');
    if (bar) bar.style.width = pct(p) + '%';
    recent.push({ el: n, ts: p.ts || 0 });
  });

  var rail = document.getElementById('continueRail');
  var row = document.querySelector('[data-row="continue"]');
  if (rail && row && recent.length) {
    recent.sort(function (a, b) { return b.ts - a.ts; });
    recent.slice(0, 12).forEach(function (r) {
      var c = r.el.cloneNode(true);
      var bar = c.querySelector('.bar i');
      if (bar) bar.style.width = pct(all[c.getAttribute('data-id')]) + '%';
      rail.appendChild(c);
    });
    row.hidden = false;
  }
})();
