import { LineChart } from "echarts/charts";
import { GridComponent, TooltipComponent } from "echarts/components";
import { init, use as registerEChartsModules } from "echarts/core";
import { CanvasRenderer } from "echarts/renderers";
import { useEffect, useRef } from "react";

registerEChartsModules([LineChart, GridComponent, TooltipComponent, CanvasRenderer]);

export function TrafficChart() {
  const container = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!container.current) return;
    const chart = init(container.current, undefined, { renderer: "canvas" });
    chart.setOption({
      animationDuration: window.matchMedia("(prefers-reduced-motion: reduce)").matches ? 0 : 300,
      grid: { left: 6, right: 8, top: 12, bottom: 22, containLabel: true },
      tooltip: { trigger: "axis", backgroundColor: "#111a25", borderColor: "#2a394b", textStyle: { color: "#e8eef6" } },
      xAxis: { type: "category", boundaryGap: false, data: ["00", "04", "08", "12", "16", "20", "24"], axisLine: { lineStyle: { color: "#293747" } }, axisLabel: { color: "#718299" }, axisTick: { show: false } },
      yAxis: { type: "value", splitNumber: 3, axisLabel: { color: "#718299", formatter: "{value}G" }, splitLine: { lineStyle: { color: "#1e2a37" } } },
      series: [{ type: "line", smooth: 0.35, showSymbol: false, data: [2.1, 2.8, 2.5, 4.1, 3.7, 5.2, 4.6], lineStyle: { color: "#49d6b0", width: 2 }, areaStyle: { color: "rgba(73, 214, 176, .10)" } }],
    });
    const resizeObserver = new ResizeObserver(() => chart.resize());
    resizeObserver.observe(container.current);
    return () => { resizeObserver.disconnect(); chart.dispose(); };
  }, []);

  return <div className="traffic-chart" ref={container} role="img" aria-label="Traffic volume over the last 24 hours" />;
}
