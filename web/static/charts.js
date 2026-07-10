(function () {
  const lineColor = '#2563eb';
  const fillColor = 'rgba(37, 99, 235, 0.08)';

  function showMessage(canvas, text) {
    const wrapper = canvas.closest('section') || canvas.parentElement;
    let msg = wrapper.querySelector('.chart-message');
    if (!msg) {
      msg = document.createElement('p');
      msg.className = 'chart-message empty-hint';
      canvas.insertAdjacentElement('afterend', msg);
    }
    msg.textContent = text;
    msg.style.display = 'block';
    canvas.style.display = 'none';
  }

  function clearMessage(canvas) {
    const wrapper = canvas.closest('section') || canvas.parentElement;
    const msg = wrapper.querySelector('.chart-message');
    if (msg) msg.style.display = 'none';
    canvas.style.display = 'block';
  }

  function setupPeriodChart(chartKey, canvasId, fetchUrl, defaultRange) {
    const canvas = document.getElementById(canvasId);
    if (!canvas) return;

    if (typeof Chart === 'undefined') {
      showMessage(canvas, 'Diagramm-Bibliothek konnte nicht geladen werden (CDN nicht erreichbar?).');
      console.error('Chart.js ist nicht geladen – Skript-Tag prüfen.');
      return;
    }

    const container = document.querySelector('.period-selector[data-chart="' + chartKey + '"]');
    let chart = null;

    async function load(range) {
      let data;
      try {
        const res = await fetch(fetchUrl + '?range=' + encodeURIComponent(range));
        if (!res.ok) {
          throw new Error('HTTP ' + res.status);
        }
        data = await res.json();
      } catch (e) {
        console.error('[' + chartKey + '] Diagrammdaten konnten nicht geladen werden:', e);
        showMessage(canvas, 'Daten konnten nicht geladen werden (' + e.message + '). Details siehe Browser-Konsole (F12).');
        return;
      }

      const points = data.points || [];
      if (points.length === 0) {
        showMessage(canvas, 'Noch keine Daten für diesen Zeitraum.');
        return;
      }
      clearMessage(canvas);

      const labels = points.map(p => p.label);
      const values = points.map(p => p.value);

      try {
        if (chart) {
          chart.data.labels = labels;
          chart.data.datasets[0].data = values;
          chart.update();
        } else {
          chart = new Chart(canvas, {
            type: 'line',
            data: {
              labels: labels,
              datasets: [{
                data: values,
                borderColor: lineColor,
                backgroundColor: fillColor,
                fill: true,
                tension: 0.15,
                pointRadius: 0,
                borderWidth: 2,
              }],
            },
            options: {
              responsive: true,
              plugins: { legend: { display: false } },
              scales: {
                x: { ticks: { maxTicksLimit: 8, autoSkip: true } },
                y: { ticks: { callback: v => Number(v).toLocaleString('de-CH') } },
              },
            },
          });
        }
      } catch (e) {
        console.error('[' + chartKey + '] Diagramm konnte nicht gezeichnet werden:', e);
        showMessage(canvas, 'Diagramm konnte nicht gezeichnet werden. Details siehe Browser-Konsole (F12).');
        return;
      }

      const hint = document.querySelector('.chart-fx-hint[data-chart="' + chartKey + '"]');
      if (hint) hint.style.display = data.hasForeignCurrency ? 'block' : 'none';
    }

    if (container) {
      container.querySelectorAll('.period-btn').forEach(btn => {
        btn.addEventListener('click', () => {
          container.querySelectorAll('.period-btn').forEach(b => b.classList.remove('active'));
          btn.classList.add('active');
          load(btn.dataset.range);
        });
      });
    }

    load(defaultRange);
  }

  function setupSankeyChart() {
    const canvas = document.getElementById('sankey-chart');
    if (!canvas) return;

    if (typeof Chart === 'undefined') {
      showMessage(canvas, 'Diagramm-Bibliothek konnte nicht geladen werden (CDN nicht erreichbar?).');
      console.error('Chart.js ist nicht geladen – Skript-Tag prüfen.');
      return;
    }

    const prevBtn = document.getElementById('sankey-prev-month');
    const nextBtn = document.getElementById('sankey-next-month');
    const label = document.getElementById('sankey-month-label');
    const emptyHint = document.getElementById('sankey-empty-hint');

    let current = new Date();
    let chart = null;

    function monthKey(d) {
      return d.getFullYear() + '-' + String(d.getMonth() + 1).padStart(2, '0');
    }

    function monthLabel(d) {
      return d.toLocaleDateString('de-CH', { month: 'long', year: 'numeric' });
    }

    async function load() {
      label.textContent = monthLabel(current);

      let data;
      try {
        const res = await fetch('/api/sankey?month=' + monthKey(current));
        if (!res.ok) {
          throw new Error('HTTP ' + res.status);
        }
        data = await res.json();
      } catch (e) {
        console.error('[sankey] Daten konnten nicht geladen werden:', e);
        canvas.style.display = 'none';
        emptyHint.textContent = 'Fehler beim Laden (' + e.message + '). Details siehe Browser-Konsole (F12).';
        emptyHint.style.display = 'block';
        return;
      }

      if (!data.flows || data.flows.length === 0) {
        canvas.style.display = 'none';
        emptyHint.textContent = 'Keine Einnahmen oder Ausgaben in diesem Monat.';
        emptyHint.style.display = 'block';
        return;
      }
      canvas.style.display = 'block';
      emptyHint.style.display = 'none';

      const chartData = {
        datasets: [{
          label: 'Budget',
          data: data.flows,
          colorFrom: '#2563eb',
          colorTo: '#16a34a',
          colorMode: 'gradient',
        }],
      };

      try {
        if (chart) {
          chart.data = chartData;
          chart.update();
        } else {
          chart = new Chart(canvas, {
            type: 'sankey',
            data: chartData,
            options: { responsive: true },
          });
        }
      } catch (e) {
        console.error('[sankey] Diagramm konnte nicht gezeichnet werden (Plugin geladen?):', e);
        canvas.style.display = 'none';
        emptyHint.textContent = 'Sankey-Diagramm konnte nicht gezeichnet werden. Details siehe Browser-Konsole (F12).';
        emptyHint.style.display = 'block';
      }
    }

    prevBtn.addEventListener('click', () => {
      current = new Date(current.getFullYear(), current.getMonth() - 1, 1);
      load();
    });
    nextBtn.addEventListener('click', () => {
      current = new Date(current.getFullYear(), current.getMonth() + 1, 1);
      load();
    });

    load();
  }

  document.addEventListener('DOMContentLoaded', function () {
    setupPeriodChart('networth', 'networth-chart', '/api/networth-history', '3m');
    setupPeriodChart('depot', 'depot-chart', '/api/depot-history', '3m');
    setupSankeyChart();
  });
})();
