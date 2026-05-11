// Phase 3 /eval real-time chart glue. Depends on Chart.js (loaded as
// <script src="/static/chart.umd.min.js">) and an EVAL_INIT object on
// window populated by the server. EventSource('/eval/stream') receives
// EvalEvents (JSON) and incrementally updates the 4 charts.
(function () {
  "use strict";
  if (!window.Chart) {
    console.warn("Chart.js missing; real-time charts disabled");
    return;
  }
  const init = window.EVAL_INIT || { scores: [], llm: [], pnl: [], buckets: [0,0,0,0,0] };

  const charts = {
    scoreScatter: makeScatter(document.getElementById("scoreScatter"), init.scores),
    llmRate:      makeLLMRate(document.getElementById("llmRate"),      init.llm),
    cumPnL:       makeCumPnL (document.getElementById("cumPnL"),       init.pnl),
    scoreBuckets: makeBuckets(document.getElementById("scoreBuckets"), init.buckets),
  };

  // Trim points older than 24h every minute so memory does not balloon.
  setInterval(function () { trimOld(charts); }, 60_000);

  const es = new EventSource("/eval/stream");
  es.addEventListener("ready", function (e) {
    console.log("eval-stream subscribed:", e.data);
  });
  es.onmessage = function (e) {
    let evt;
    try { evt = JSON.parse(e.data); } catch (err) { return; }
    applyEvent(charts, evt);
  };

  // --------- chart constructors ---------

  function makeScatter(canvas, points) {
    if (!canvas) return null;
    const data = (points || []).map(p => ({ x: p.t * 1000, y: p.score, decision: p.decision, symbol: p.symbol }));
    return new Chart(canvas, {
      type: "scatter",
      data: { datasets: [{ label: "Agent Score", data: data, pointRadius: 3 }] },
      options: {
        scales: {
          x: { type: "linear", title: { display: true, text: "时间 (unix ms)" } },
          y: { min: 0, max: 100, title: { display: true, text: "Score" } },
        },
        plugins: { legend: { display: false } },
      },
    });
  }

  function makeLLMRate(canvas, points) {
    if (!canvas) return null;
    const labels = (points || []).map(p => new Date(p.t * 1000).toISOString().slice(11, 16));
    const data = (points || []).map(p => p.total ? (100 * p.successful / p.total) : 0);
    return new Chart(canvas, {
      type: "line",
      data: { labels: labels, datasets: [{ label: "LLM Success %", data: data, tension: 0.3 }] },
      options: { scales: { y: { min: 0, max: 100 } } },
    });
  }

  function makeCumPnL(canvas, points) {
    if (!canvas) return null;
    const labels = (points || []).map(p => new Date(p.t * 1000).toISOString().slice(11, 16));
    const data = (points || []).map(p => p.cum);
    return new Chart(canvas, {
      type: "line",
      data: { labels: labels, datasets: [{ label: "Cumulative PnL ($)", data: data, fill: false, tension: 0.2 }] },
    });
  }

  function makeBuckets(canvas, buckets) {
    if (!canvas) return null;
    return new Chart(canvas, {
      type: "bar",
      data: {
        labels: ["0-20", "20-40", "40-60", "60-80", "80-100"],
        datasets: [{ label: "Score Buckets (24h)", data: (buckets || [0,0,0,0,0]).slice() }],
      },
      options: { plugins: { legend: { display: false } } },
    });
  }

  // --------- live event handling ---------

  function applyEvent(charts, evt) {
    if (evt.kind === "agent_score" && evt.agent_score != null) {
      pushScatterPoint(charts.scoreScatter, evt);
      bumpBucket(charts.scoreBuckets, evt.agent_score);
      bumpLLMRate(charts.llmRate, evt);
    } else if (evt.kind === "trade_closed" && evt.pnl_usdc != null) {
      addPnLPoint(charts.cumPnL, evt);
    }
  }

  function pushScatterPoint(chart, evt) {
    if (!chart) return;
    chart.data.datasets[0].data.push({
      x: evt.occurred_at * 1000, y: evt.agent_score,
      decision: evt.decision, symbol: evt.symbol,
    });
    chart.update("none");
  }

  function bumpBucket(chart, score) {
    if (!chart) return;
    const idx = score < 20 ? 0 : score < 40 ? 1 : score < 60 ? 2 : score < 80 ? 3 : 4;
    chart.data.datasets[0].data[idx]++;
    chart.update("none");
  }

  function bumpLLMRate(chart, evt) {
    if (!chart) return;
    const label = new Date(evt.occurred_at * 1000).toISOString().slice(11, 16);
    const success = evt.decision && evt.decision !== "failed" ? 100 : 0;
    chart.data.labels.push(label);
    chart.data.datasets[0].data.push(success);
    chart.update("none");
  }

  function addPnLPoint(chart, evt) {
    if (!chart) return;
    const prev = chart.data.datasets[0].data;
    const last = prev.length ? prev[prev.length - 1] : 0;
    chart.data.labels.push(new Date(evt.occurred_at * 1000).toISOString().slice(11, 16));
    prev.push(last + evt.pnl_usdc);
    chart.update("none");
  }

  function trimOld(charts) {
    const cutoffMs = Date.now() - 24 * 3600 * 1000;
    if (charts.scoreScatter) {
      const ds = charts.scoreScatter.data.datasets[0];
      ds.data = ds.data.filter(p => p.x >= cutoffMs);
      charts.scoreScatter.update("none");
    }
    for (const name of ["llmRate", "cumPnL"]) {
      const c = charts[name];
      if (!c) continue;
      const max = 24 * 60;
      while (c.data.labels.length > max) {
        c.data.labels.shift();
        c.data.datasets[0].data.shift();
      }
      c.update("none");
    }
  }
})();
