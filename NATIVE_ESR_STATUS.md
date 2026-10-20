# E36731A 原生硬體 ESR — 尚未完成

本版不能設定或讀回原生硬體 ESR。PS2Q 雙向模式的存在，不足以證明已取得硬體 ESR 介面；沒有把電子負載 CR 或電腦端調壓當作硬體 ESR。

## 已核對的原廠資料

- [Programming Guide，E36731-90009，Edition 2，August 2026](https://www.keysight.com/us/en/assets/9923-01316/programming-guides/E36731A-Battery-Emulator-Programming-Guide.pdf)：第 64 頁定義 PSUP、LOAD、PS2Q，切換會重設；第 103 頁的 RES 是電子負載 CR 輸入電阻。未在此手冊找到原生串聯輸出電阻的設定、啟用與讀回指令。
- [原廠規格表，第 12 頁](https://www.keysight.com/tw/zh/assets/3123-1042/data-sheets/E36731A-Battery-emulator-and-profiler.pdf)：列有可程控輸出電阻 −50 mΩ 至 1 kΩ。但此表未提供其控制命令、PS2Q 雙向適用條件與內阻控制頻寬，因此不能直接據此完成實作。
- [原廠電池模擬軟體說明](https://helpfiles.keysight.com/Standalone_BenchVueSoftware_PW_HelpFiles/PWPowerAppSuite/Content/Battery%20Test%20and%20Emulation/Applications/Emulate_Function.htm)：描述以儀器取得的電流陣列與模型 Ri 計算端電壓，並有模擬速度選項。這不能用來證明 E36731A 存在可獨立設定的硬體 ESR 控制迴路。
- [原廠產品頁](https://www.keysight.com/us/en/product/E36731A/battery-emulator-and-profiler.html)確認最新韌體支援原生雙象限運作；沒有因此將「雙向」等同於「硬體 ESR」。
- [原廠韌體頁](https://www.keysight.com/us/en/lib/software-detail/instrument-firmware-software/e36731a-firmware.html)列出的 K-01.14.03-01.00-02.14-01.06 與現場回報版本一致。不能沒有證據就把升級韌體當作 ESR 解法。

部分官方 PDF 下載網址目前回傳 HTML 資源頁。程式設計手冊的本機 PDF 已核對；規格與軟體功能也已核對官方網頁／索引內容。沒有從未取得的更新說明全文推論新功能。

## 需要 Keysight 確認的具體問題

E36731A、韌體 K-01.14.03-01.00-02.14-01.06，在 EMUL PS2Q 下，能否設定由儀器內部控制的固定串聯輸出電阻，例如 0.010 Ω，並在 Source 與 Sink 兩個方向作用，無需電腦週期性修改電壓？

若可以，需提供：

1. 設定／讀回阻值、啟用／讀回狀態的完整 SCPI，單位、範圍及解析度。
2. 支援的模式、韌體與授權條件，以及切換模式／斷開電腦後的行為。
3. Source 與 Sink 的獨立硬體限流設定／讀回方式。
4. ESR 控制迴路頻寬或階躍響應規格、負載電容與穩定度條件。

若規格表的輸出電阻實際由原廠電腦軟體實現，也需明確確認；不能用低速軟體模型滿足硬體即時 ESR 要求。

## 取得介面後的實作方式

在輸出 OFF 時檢查模式與適用條件，將 mΩ 轉成 Ω 後設定，逐條檢查新錯誤，再讀回啟用狀態與阻值。內阻讀回容差需依原廠解析度，不能沿用電壓的絕對容差。成功前禁止 ON，失敗不退回軟體模擬。最後需用正／負電流階躍實測 ΔV/ΔI 與響應時間。

上述是待取得文件後的整合方案，**本版並未實作或宣稱完成**。
