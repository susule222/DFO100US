using System;
using System.Diagnostics;
using System.IO;
using System.Windows.Forms;

internal static class Launcher {
    [STAThread]
    static void Main() {
        var root = AppDomain.CurrentDomain.BaseDirectory;
        var script = Path.Combine(root, "tools", "atelier", "bootstrap.pyw");
        try {
            if (!File.Exists(script)) throw new FileNotFoundException(script);
            Process.Start(new ProcessStartInfo("pythonw.exe", "\"" + script + "\"") {
                WorkingDirectory = root, UseShellExecute = false, CreateNoWindow = true
            });
        } catch (Exception e) {
            MessageBox.Show("无法打开登录器，请确认工程目录完整，且 Python 已加入 PATH。\n" + e.Message,
                            "阿拉德星图", MessageBoxButtons.OK, MessageBoxIcon.Error);
        }
    }
}
