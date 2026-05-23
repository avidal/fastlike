// fastlike profile timeline viewer.
//
// Reads /r/{req_id}.json from the URL the template hands us via the
// data-json-url attribute on #fl-timeline, and renders two horizontal
// tracks (hostcalls, backend calls) on a single canvas. No external
// dependencies, no framework, ~250 lines including comments. The
// server-rendered tables below this canvas are the authoritative no-JS
// fallback; if this script fails to load or run, the page still works.
(function () {
  'use strict';

  var ROOT_ID = 'fl-timeline';
  var TRACK_HEIGHT = 28;
  var TRACK_GAP = 4;
  var LABEL_WIDTH = 90;
  var PAD_TOP = 6;
  var PAD_BOTTOM = 26;
  // TRACK_COUNT changes based on whether the trace carries
  // native_samples. The renderer computes it per-trace so the canvas
  // height stays minimal when no sampling data is present.

  document.addEventListener('DOMContentLoaded', init);

  function init() {
    var root = document.getElementById(ROOT_ID);
    if (!root) return;
    var jsonURL = root.getAttribute('data-json-url');
    if (!jsonURL) return;

    fetch(jsonURL, { credentials: 'same-origin' })
      .then(function (resp) {
        if (!resp.ok) throw new Error('HTTP ' + resp.status);
        return resp.json();
      })
      .then(function (trace) { render(root, trace); })
      .catch(function (err) {
        var note = document.createElement('div');
        note.style.color = '#b00020';
        note.style.fontSize = '12px';
        note.style.marginTop = '0.25rem';
        note.textContent = 'Timeline failed to load: ' + err.message;
        root.appendChild(note);
      });
  }

  function render(root, trace) {
    while (root.firstChild) root.removeChild(root.firstChild);

    var hasNativeSamples = Array.isArray(trace.native_samples) && trace.native_samples.length > 0;
    var trackCount = hasNativeSamples ? 3 : 2;

    var containerW = root.clientWidth || 800;
    var canvasW = Math.max(containerW, 600);
    var canvasH = PAD_TOP + trackCount * (TRACK_HEIGHT + TRACK_GAP) + PAD_BOTTOM;

    var dpr = window.devicePixelRatio || 1;
    var canvas = document.createElement('canvas');
    canvas.width = canvasW * dpr;
    canvas.height = canvasH * dpr;
    canvas.style.width = canvasW + 'px';
    canvas.style.height = canvasH + 'px';
    canvas.style.display = 'block';
    canvas.style.background = '#fff';
    canvas.style.border = '1px solid #e3e3e3';
    canvas.style.borderRadius = '3px';
    canvas.style.marginTop = '0.5rem';

    var ctx = canvas.getContext('2d');
    ctx.scale(dpr, dpr);
    ctx.font = '11px ui-monospace, SFMono-Regular, Menlo, monospace';
    ctx.textBaseline = 'middle';

    var totalNanos = Math.max(trace.wall_nanos || 1, 1);
    var innerX = LABEL_WIDTH;
    var innerW = canvasW - LABEL_WIDTH - 8;

    function xOf(nanos) {
      return innerX + (Math.min(nanos, totalNanos) / totalNanos) * innerW;
    }

    for (var ti = 0; ti < trackCount; ti++) drawTrackBackground(ctx, ti, innerX, innerW);

    ctx.fillStyle = '#444';
    ctx.fillText('hostcalls', 4, rowY(0) + TRACK_HEIGHT / 2);
    ctx.fillText('backends',  4, rowY(1) + TRACK_HEIGHT / 2);
    if (hasNativeSamples) {
      ctx.fillText('native', 4, rowY(2) + TRACK_HEIGHT / 2);
    }

    var spans = trace.spans || [];
    for (var i = 0; i < spans.length; i++) {
      var s = spans[i];
      var start = s.start_nanos || 0;
      var dur   = s.duration_nanos || 0;
      var x = xOf(start);
      var w = Math.max(xOf(start + dur) - x, 1);
      ctx.fillStyle = colorForSpan(s.rc);
      ctx.fillRect(x, rowY(0), w, TRACK_HEIGHT);
    }

    var calls = trace.backend_calls || [];
    for (var j = 0; j < calls.length; j++) {
      var c = calls[j];
      var cstart = c.started_nanos || 0;
      var ctotal = c.total_nanos || 0;
      var cx = xOf(cstart);
      var cw = Math.max(xOf(cstart + ctotal) - cx, 1);
      ctx.fillStyle = colorForBackend(c.outcome);
      ctx.fillRect(cx, rowY(1), cw, TRACK_HEIGHT);

      // Label the bar with the backend name when it fits.
      var label = c.name || c.url_redacted || '';
      if (label && cw > 40) {
        ctx.fillStyle = '#fff';
        var trunc = truncateLabel(ctx, label, cw - 8);
        ctx.fillText(trunc, cx + 4, rowY(1) + TRACK_HEIGHT / 2);
      }
    }

    if (hasNativeSamples) {
      drawNativeSamples(ctx, trace.native_samples, xOf);
    }

    drawAxis(ctx, innerX, innerW, totalNanos, trackCount);
    drawHeaderFlushMarker(ctx, trace, xOf, trackCount);

    root.appendChild(canvas);

    if (trace.dropped || trace.dropped_backend_calls) {
      var banner = document.createElement('div');
      banner.style.color = '#c75800';
      banner.style.fontSize = '12px';
      banner.style.marginTop = '0.25rem';
      var parts = [];
      if (trace.dropped) parts.push(trace.dropped + ' spans dropped');
      if (trace.dropped_backend_calls) parts.push(trace.dropped_backend_calls + ' backend calls dropped');
      banner.textContent = 'truncated: ' + parts.join(', ');
      root.appendChild(banner);
    }
  }

  function rowY(rowIdx) {
    return PAD_TOP + rowIdx * (TRACK_HEIGHT + TRACK_GAP);
  }

  function drawTrackBackground(ctx, rowIdx, innerX, innerW) {
    ctx.fillStyle = '#f6f6f8';
    ctx.fillRect(innerX, rowY(rowIdx), innerW, TRACK_HEIGHT);
  }

  function drawNativeSamples(ctx, samples, xOf) {
    var y = rowY(2);
    ctx.fillStyle = '#2a9d4a';
    for (var i = 0; i < samples.length; i++) {
      var rel = samples[i].relative_nanos || 0;
      var x = xOf(rel);
      // 2px tick covering the full track height.
      ctx.fillRect(x - 1, y, 2, TRACK_HEIGHT);
    }
  }

  function drawAxis(ctx, innerX, innerW, totalNanos, trackCount) {
    var baseY = PAD_TOP + trackCount * (TRACK_HEIGHT + TRACK_GAP) + 10;
    ctx.strokeStyle = '#aaa';
    ctx.lineWidth = 1;
    ctx.beginPath();
    ctx.moveTo(innerX, baseY);
    ctx.lineTo(innerX + innerW, baseY);
    ctx.stroke();

    ctx.fillStyle = '#666';
    var tickCount = 6;
    for (var i = 0; i <= tickCount; i++) {
      var frac = i / tickCount;
      var x = innerX + frac * innerW;
      ctx.beginPath();
      ctx.moveTo(x, baseY);
      ctx.lineTo(x, baseY + 4);
      ctx.stroke();
      var ms = (frac * totalNanos) / 1e6;
      var label = ms.toFixed(2) + ' ms';
      var textW = ctx.measureText(label).width;
      ctx.fillText(label, x - textW / 2, baseY + 14);
    }
  }

  function drawHeaderFlushMarker(ctx, trace, xOf, trackCount) {
    if (typeof trace.header_flush_nanos !== 'number') return;
    var x = xOf(trace.header_flush_nanos);
    ctx.strokeStyle = '#2a9d4a';
    ctx.lineWidth = 1;
    ctx.setLineDash([3, 3]);
    ctx.beginPath();
    ctx.moveTo(x, PAD_TOP);
    ctx.lineTo(x, PAD_TOP + trackCount * (TRACK_HEIGHT + TRACK_GAP));
    ctx.stroke();
    ctx.setLineDash([]);
    ctx.fillStyle = '#2a9d4a';
    ctx.fillText('header flush', x + 3, PAD_TOP + 6);
  }

  function colorForSpan(rc) {
    if (rc && rc !== 0) return '#b00020';
    return '#7691e0';
  }

  function colorForBackend(outcome) {
    switch (outcome) {
      case 'ok':                  return '#4a7dff';
      case 'synthetic-failure':   return '#c75800';
      case 'network-error':       return '#b00020';
      case 'cancelled':           return '#888';
      case 'orphaned':            return '#888';
      case 'incomplete':          return '#444';
      default:                    return '#4a7dff';
    }
  }

  function truncateLabel(ctx, text, maxWidth) {
    if (ctx.measureText(text).width <= maxWidth) return text;
    var ellipsis = '…';
    var lo = 0, hi = text.length;
    while (lo < hi) {
      var mid = Math.ceil((lo + hi) / 2);
      var candidate = text.slice(0, mid) + ellipsis;
      if (ctx.measureText(candidate).width <= maxWidth) lo = mid;
      else hi = mid - 1;
    }
    return text.slice(0, lo) + ellipsis;
  }
})();
