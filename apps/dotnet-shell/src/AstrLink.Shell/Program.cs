using AstrLink.Shell;
using AstrLink.Shell.Tray;

namespace AstrLink.Shell.Tray;

static class Program
{
    [STAThread]
    static void Main()
    {
        ApplicationConfiguration.Initialize();
        Application.Run(new TrayApplicationContext(ShellOptions.FromEnvironment()));
    }
}
