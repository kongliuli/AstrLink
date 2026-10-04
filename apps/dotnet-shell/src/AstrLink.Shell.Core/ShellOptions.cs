namespace AstrLink.Shell;

public sealed class ShellOptions
{
    public required string CoreExecutable { get; init; }
    public string? ClassifierWorker { get; init; }
    public string? PrivacyWorker { get; init; }
    public required string DataDirectory { get; init; }
    public required string HomeDirectory { get; init; }
    public ushort InferencePort { get; init; } = 8317;
    public ushort MaxConcurrentInspections { get; init; } = 16;
    public uint ResponseStartTimeoutSeconds { get; init; }
    public uint MaxRequestBodyMiB { get; init; }
    public bool UseSystemProxy { get; init; } = true;
    public string? DesktopExecutable { get; init; }
    public string? IntentModelDirectory { get; init; }

    public static ShellOptions FromEnvironment(string? root = null)
    {
        root ??= AppContext.BaseDirectory;
        var home = Path.Combine(
            Environment.GetFolderPath(Environment.SpecialFolder.UserProfile),
            ".astrlink");
        var data = Path.Combine(
            Environment.GetFolderPath(Environment.SpecialFolder.ApplicationData),
            "com.astrlink.desktop");
        if (OperatingSystem.IsWindows())
        {
            data = Path.Combine(
                Environment.GetFolderPath(Environment.SpecialFolder.ApplicationData),
                "com.astrlink.desktop");
        }
        else
        {
            data = Path.Combine(home, "data");
        }

        string Resolve(string name) =>
            FirstExisting(
                Path.Combine(root, name),
                Path.Combine(root, "bin", name),
                Path.Combine(Directory.GetCurrentDirectory(), "bin", name)) ?? Path.Combine(root, "bin", name);

        var exe = OperatingSystem.IsWindows() ? ".exe" : "";
        return new ShellOptions
        {
            CoreExecutable = Resolve($"astrlink-core{exe}"),
            ClassifierWorker = Resolve($"astrlink-classifier-worker{exe}"),
            PrivacyWorker = Resolve($"astrlink-privacy-worker{exe}"),
            DataDirectory = data,
            HomeDirectory = home,
            DesktopExecutable = FirstExisting(
                Path.Combine(root, $"AstrLink{exe}"),
                Path.Combine(root, "desktop", $"AstrLink{exe}"),
                Path.Combine(root, "desktop", "astrlink-desktop.exe"))
                ?? FindBuiltDesktop(root),
            IntentModelDirectory = ResolveIntentModelDirectory(root)
                ?? ResolveIntentModelDirectory(Directory.GetCurrentDirectory()),
        };
    }

    public static string? FindBuiltDesktop(string start)
    {
        var dir = new DirectoryInfo(start);
        for (var depth = 0; depth < 8 && dir is not null; depth++, dir = dir.Parent)
        {
            var candidate = Path.Combine(
                dir.FullName,
                "apps",
                "desktop",
                "src-tauri",
                "target",
                "release",
                "astrlink-desktop.exe");
            if (File.Exists(candidate))
            {
                return candidate;
            }
        }

        return null;
    }

    /// <summary>
    /// The bundled package lives at AstrLink.Shell/models/astrlink-intent-v1.
    /// Core rejects a path that passes through a directory junction, so this
    /// returns the physical directory.
    /// </summary>
    public static string? ResolveIntentModelDirectory(string? start)
    {
        if (string.IsNullOrWhiteSpace(start))
        {
            return null;
        }

        if (IsModelPackage(start))
        {
            return Physical(start);
        }

        var dir = new DirectoryInfo(start);
        for (var depth = 0; depth < 8 && dir is not null; depth++, dir = dir.Parent)
        {
            var candidate = Path.Combine(dir.FullName, "models", "astrlink-intent-v1");
            if (IsModelPackage(candidate))
            {
                return Physical(candidate);
            }
        }

        return null;
    }

    static bool IsModelPackage(string path) =>
        File.Exists(Path.Combine(path, "model.onnx")) &&
        File.Exists(Path.Combine(path, "tokenizer.json"));

    static string Physical(string path)
    {
        var full = Path.GetFullPath(path);
        var root = Path.GetPathRoot(full);
        if (string.IsNullOrEmpty(root))
        {
            return full;
        }

        var built = root;
        foreach (var part in full[root.Length..].Split(Path.DirectorySeparatorChar, Path.AltDirectorySeparatorChar))
        {
            if (part.Length == 0)
            {
                continue;
            }

            built = Path.Combine(built, part);
            var info = new DirectoryInfo(built);
            if (!info.Exists || (info.Attributes & FileAttributes.ReparsePoint) == 0)
            {
                continue;
            }

            var target = info.LinkTarget;
            if (string.IsNullOrEmpty(target))
            {
                continue;
            }

            built = Path.IsPathRooted(target)
                ? target
                : Path.GetFullPath(Path.Combine(info.Parent?.FullName ?? root, target));
        }

        return built;
    }

    static string? FirstExisting(params string[] paths) =>
        paths.FirstOrDefault(File.Exists) ?? paths.FirstOrDefault(Directory.Exists);
}
